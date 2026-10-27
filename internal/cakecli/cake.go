// Package cakecli is the process boundary between cake-suggest and Cake.
//
// It runs only documented Cake commands: the read-only
// `cake --output-format stream-json replay <uuid>` stream, and one ordinary
// `cake --no-session --no-tools --no-skills --output-format json` completion.
// It never reads or writes Cake session files.
package cakecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// defaultMaxReplayBytes bounds the replay stream this tool will buffer. A
	// suggestion needs only the recent transcript; a session larger than this
	// should be compacted or continued in a new session.
	defaultMaxReplayBytes = 64 << 20
	// maxCompletionBytes bounds one completion JSON document.
	maxCompletionBytes = 4 << 20
	// maxStderrBytes bounds the retained diagnostic tail.
	maxStderrBytes = 8 << 10
)

// ResolveBinary returns the Cake executable to use. An explicit path or a
// value from CAKE_BIN wins over PATH.
func ResolveBinary(override string) (string, error) {
	binary := override
	if binary == "" {
		binary = os.Getenv("CAKE_BIN")
	}
	if binary == "" {
		binary = "cake"
	}
	if strings.ContainsRune(binary, filepath.Separator) {
		return filepath.Abs(binary)
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", fmt.Errorf("cannot find the cake executable %q: %w", binary, err)
	}
	return path, nil
}

// ResolveDataDir returns the Cake data root. It matches Cake's own resolution:
// an explicit override, then CAKE_DATA_DIR, then ~/.local/share/cake.
func ResolveDataDir(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	if env := os.Getenv("CAKE_DATA_DIR"); env != "" {
		return filepath.Abs(env)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate the home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "cake"), nil
}

// ReplayOptions describes one read-only session replay.
type ReplayOptions struct {
	Binary    string
	DataDir   string
	SessionID string
	MaxBytes  int64
}

// Replay returns the raw NDJSON replay stream. The command reads the session
// read-only: no lock, no append, and no model call.
func Replay(ctx context.Context, options ReplayOptions) ([]byte, error) {
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxReplayBytes
	}
	cmd := exec.CommandContext(ctx, options.Binary,
		"--output-format", "stream-json",
		"replay", options.SessionID,
	)
	if options.DataDir != "" {
		cmd.Env = environmentWith("CAKE_DATA_DIR", options.DataDir)
	}

	stdout := &capBuffer{limit: maxBytes}
	stderr := &tailBuffer{limit: maxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return nil, replayError(ctx, err, stderr.String())
	}
	if stdout.overflow {
		return nil, fmt.Errorf(
			"cake replay output exceeded %d bytes; compact the session or start a new one",
			maxBytes,
		)
	}
	return stdout.Bytes(), nil
}

func replayError(ctx context.Context, err error, stderr string) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("cake replay timed out: %w", ctx.Err())
	case ctx.Err() != nil:
		return fmt.Errorf("cake replay canceled: %w", ctx.Err())
	}
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("cake replay failed: %s", detail)
}

// Usage is the provider-reported token usage for one Cake invocation. A zero
// value means the response carried no usage, not zero tokens.
type Usage struct {
	InputTokens  uint64 `json:"input_tokens"`
	OutputTokens uint64 `json:"output_tokens"`
	TotalTokens  uint64 `json:"total_tokens"`
}

// Completion is the subset of Cake's --output-format json document this tool
// reads.
type Completion struct {
	Result *string         `json:"result"`
	Error  *string         `json:"error"`
	Usage  json.RawMessage `json:"usage"`
}

// ModelOptions describes one isolated suggestion model call.
type ModelOptions struct {
	Binary       string
	Model        string
	Profile      string
	SystemPrompt string
	OutputSchema string
	Transcript   string
}

// Suggest asks a tool-less, session-less Cake invocation for a suggestion.
//
// The child runs in a temporary directory with a temporary data root, so the
// call cannot pollute the user's sessions, telemetry, project settings, hooks,
// or skills. The prompt and schema are written to that directory; the
// transcript is supplied on stdin.
func Suggest(ctx context.Context, options ModelOptions) (string, Usage, error) {
	var usage Usage

	tempDir, err := os.MkdirTemp("", "cake-suggest-model-*")
	if err != nil {
		return "", usage, fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	promptPath := filepath.Join(tempDir, "system-prompt.txt")
	if err := os.WriteFile(promptPath, []byte(options.SystemPrompt), 0o600); err != nil {
		return "", usage, fmt.Errorf("write system prompt: %w", err)
	}
	schemaPath := filepath.Join(tempDir, "output-schema.json")
	if err := os.WriteFile(schemaPath, []byte(options.OutputSchema), 0o600); err != nil {
		return "", usage, fmt.Errorf("write output schema: %w", err)
	}
	dataDir := filepath.Join(tempDir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", usage, fmt.Errorf("create temporary cake data directory: %w", err)
	}

	args := []string{
		"--no-session",
		"--no-tools",
		"--no-skills",
		"--output-format", "json",
		"--system-prompt", promptPath,
		"--output-schema", schemaPath,
	}
	if options.Model != "" {
		args = append(args, "--model", options.Model)
	}
	if options.Profile != "" {
		args = append(args, "--profile", options.Profile)
	}
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, options.Binary, args...)
	cmd.Dir = tempDir
	cmd.Env = environmentWith("CAKE_DATA_DIR", dataDir)
	cmd.Stdin = strings.NewReader(options.Transcript)

	stdout := &capBuffer{limit: maxCompletionBytes}
	stderr := &tailBuffer{limit: maxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return "", usage, modelError(ctx, err, stderr.String(), stdout.String())
	}
	if stdout.overflow {
		return "", usage, fmt.Errorf("suggestion completion exceeded %d bytes", maxCompletionBytes)
	}

	var completion Completion
	if err := json.Unmarshal(stdout.Bytes(), &completion); err != nil {
		return "", usage, fmt.Errorf(
			"suggestion returned invalid completion JSON: %w: %s",
			err, strings.TrimSpace(stdout.String()),
		)
	}
	if len(completion.Usage) > 0 {
		// Usage is diagnostic: an unfamiliar shape must not fail a suggestion
		// that otherwise succeeded.
		_ = json.Unmarshal(completion.Usage, &usage)
	}
	if completion.Error != nil && strings.TrimSpace(*completion.Error) != "" {
		return "", usage, fmt.Errorf("suggestion failed: %s", strings.TrimSpace(*completion.Error))
	}
	if completion.Result == nil || strings.TrimSpace(*completion.Result) == "" {
		return "", usage, errors.New("suggestion returned an empty result")
	}
	return strings.TrimSpace(*completion.Result), usage, nil
}

func modelError(ctx context.Context, err error, stderr, stdout string) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("suggestion model call timed out: %w", ctx.Err())
	case ctx.Err() != nil:
		return fmt.Errorf("suggestion model call canceled: %w", ctx.Err())
	}
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = strings.TrimSpace(stdout)
	}
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("suggestion model call failed: %s", detail)
}

// capBuffer keeps at most limit bytes and records whether more arrived. It
// keeps the head, because a completion document is parsed from byte zero.
type capBuffer struct {
	limit    int64
	buf      bytes.Buffer
	overflow bool
}

func (w *capBuffer) Write(p []byte) (int, error) {
	room := w.limit - int64(w.buf.Len())
	if room <= 0 {
		w.overflow = true
		return len(p), nil
	}
	if int64(len(p)) > room {
		_, _ = w.buf.Write(p[:room])
		w.overflow = true
		return len(p), nil
	}
	_, _ = w.buf.Write(p)
	return len(p), nil
}

func (w *capBuffer) Bytes() []byte  { return w.buf.Bytes() }
func (w *capBuffer) String() string { return w.buf.String() }

// tailBuffer retains the last limit bytes of diagnostics.
type tailBuffer struct {
	limit int
	buf   []byte
}

func (w *tailBuffer) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.limit {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-w.limit:]...)
	}
	return len(p), nil
}

func (w *tailBuffer) String() string { return string(w.buf) }

// environmentWith returns the process environment with key set to value,
// removing duplicate assignments so the child sees the intended value.
func environmentWith(key, value string) []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env)+1)
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, prefix+value)
}

var _ io.Writer = (*capBuffer)(nil)
var _ io.Writer = (*tailBuffer)(nil)
