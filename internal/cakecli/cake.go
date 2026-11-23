// Package cakecli is the process boundary between cake-suggest and Cake.
//
// It runs only documented Cake commands: the read-only
// `cake --output-format stream-json replay <uuid>` stream, an exact request
// reuse, a read-only `cake --resume <uuid>` completion, and a tool-less,
// session-less fallback completion. It never reads or writes Cake session
// files.
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
	"regexp"
	"strings"
)

var sessionIDPattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
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

// ResolveCacheDir returns the Cake cache root. When a data root is configured
// explicitly or through CAKE_DATA_DIR, Cake uses it for both cache and session
// data. Otherwise the cache defaults to ~/.cache/cake while sessions stay in
// ~/.local/share/cake/sessions.
func ResolveCacheDir(dataDir string, dataDirConfigured bool) (string, error) {
	if dataDirConfigured || os.Getenv("CAKE_DATA_DIR") != "" {
		return dataDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate the home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "cake"), nil
}

// ValidSessionID reports whether id is the lowercase UUID Cake uses for
// session identity. It also keeps a caller-supplied value from escaping the
// capture directory path.
func ValidSessionID(id string) bool {
	return sessionIDPattern.MatchString(id)
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
	InputTokens        uint64             `json:"input_tokens"`
	InputTokensDetails InputTokensDetails `json:"input_tokens_details"`
	OutputTokens       uint64             `json:"output_tokens"`
	TotalTokens        uint64             `json:"total_tokens"`
}

// InputTokensDetails is the provider's prompt-cache breakdown. Providers may
// omit either counter; a missing field decodes as zero.
type InputTokensDetails struct {
	CachedTokens     uint64 `json:"cached_tokens"`
	CacheWriteTokens uint64 `json:"cache_write_tokens"`
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
	Binary          string
	Model           string
	Profile         string
	ReasoningEffort string
	SystemPrompt    string
	OutputSchema    string
	Transcript      string
}

// Suggest asks a tool-less, session-less Cake invocation for a suggestion.
//
// This is the fallback path. The child runs in a temporary directory with a
// temporary data root and a temporary Cake config root that contains only the
// global settings.toml. It therefore cannot pollute the user's sessions,
// telemetry, project settings, hooks, MCP servers, skills, or toolbox tools.
// The prompt and schema are written to that directory; the transcript is
// supplied on stdin.
func Suggest(ctx context.Context, options ModelOptions) (string, Usage, error) {
	tempDir, err := os.MkdirTemp("", "cake-suggest-model-*")
	if err != nil {
		return "", Usage{}, fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	configRoot, err := isolatedConfigDir(tempDir)
	if err != nil {
		return "", Usage{}, err
	}

	promptPath := filepath.Join(tempDir, "system-prompt.txt")
	if err := os.WriteFile(promptPath, []byte(options.SystemPrompt), 0o600); err != nil {
		return "", Usage{}, fmt.Errorf("write system prompt: %w", err)
	}
	schemaPath, err := writeOutputSchema(tempDir, options.OutputSchema)
	if err != nil {
		return "", Usage{}, err
	}
	dataDir := filepath.Join(tempDir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", Usage{}, fmt.Errorf("create temporary cake data directory: %w", err)
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
	if options.ReasoningEffort != "" {
		args = append(args, "--reasoning-effort", options.ReasoningEffort)
	}
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, options.Binary, args...)
	cmd.Dir = tempDir
	cmd.Env = environmentWith(
		"CAKE_DATA_DIR", dataDir,
		"XDG_CONFIG_HOME", configRoot,
	)
	cmd.Stdin = strings.NewReader(options.Transcript)
	return runCompletion(ctx, cmd, "suggestion model call")
}

// isolatedConfigDir creates a temporary Cake config root with a copy of the
// user's global settings.toml. The excerpt call needs model and profile
// definitions, but it must not load global hooks, MCP servers, skills, or
// toolbox tools.
func isolatedConfigDir(tempDir string) (string, error) {
	root := filepath.Join(tempDir, "config")
	cakeDir := filepath.Join(root, "cake")
	if err := os.MkdirAll(cakeDir, 0o700); err != nil {
		return "", fmt.Errorf("create isolated Cake config directory: %w", err)
	}

	sourceRoot := os.Getenv("XDG_CONFIG_HOME")
	if sourceRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot locate the home directory: %w", err)
		}
		sourceRoot = filepath.Join(home, ".config")
	}
	source := filepath.Join(sourceRoot, "cake", "settings.toml")
	settings, err := os.ReadFile(source)
	if errors.Is(err, os.ErrNotExist) {
		return root, nil
	}
	if err != nil {
		return "", fmt.Errorf("read Cake settings %q: %w", source, err)
	}
	path := filepath.Join(cakeDir, "settings.toml")
	if err := os.WriteFile(path, settings, 0o600); err != nil {
		return "", fmt.Errorf("write isolated Cake settings: %w", err)
	}
	return root, nil
}

// ResumeOptions describes one read-only resumed suggestion call.
type ResumeOptions struct {
	Binary           string
	DataDir          string
	SessionID        string
	WorkingDirectory string
	Model            string
	Profile          string
	ReasoningEffort  string
	OutputSchema     string
	Instructions     string
}

// ReuseOptions describes one exact request-reuse or read-only resume call.
type ReuseOptions = ResumeOptions

// Resume asks a read-only resumed Cake invocation for a suggestion.
//
// The child runs in the session's own working directory against the real data
// root, so it sees the stored conversation and rebuilds the project prompt
// from that directory. --read-only suppresses records, telemetry, hooks, and
// the writer lock. --tool-choice none keeps the tool set in the request,
// preserving the prompt prefix, while forbidding tool calls. The instructions
// are supplied as a final user message; no system prompt is replaced and no
// session flag hides the stored history.
func Resume(ctx context.Context, options ResumeOptions) (string, Usage, error) {
	return resumedCompletion(ctx, options, false)
}

// Reuse asks Cake to send the captured next-request body for one suggestion.
//
// --reuse-last-request implies --read-only, so this path does not pass that
// flag. It also does not pass --tool-choice: Cake preserves the captured
// request's choice, and a returned tool call is rejected before dispatch. The
// working directory may be empty when the stored session directory is
// unavailable; reuse bypasses project context, so the child then inherits
// cake-suggest's directory.
func Reuse(ctx context.Context, options ReuseOptions) (string, Usage, error) {
	return resumedCompletion(ctx, options, true)
}

func resumedCompletion(ctx context.Context, options ResumeOptions, reuse bool) (string, Usage, error) {
	if !reuse && options.WorkingDirectory == "" {
		return "", Usage{}, errors.New("session has no working directory")
	}
	tempDir, err := os.MkdirTemp("", "cake-suggest-resume-*")
	if err != nil {
		return "", Usage{}, fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	schemaPath, err := writeOutputSchema(tempDir, options.OutputSchema)
	if err != nil {
		return "", Usage{}, err
	}

	args := []string{"--resume", options.SessionID}
	if reuse {
		args = append(args, "--reuse-last-request")
	} else {
		args = append(args, "--read-only", "--tool-choice", "none")
	}
	args = append(args,
		"--output-format", "json",
		"--output-schema", schemaPath,
	)
	if options.Model != "" {
		args = append(args, "--model", options.Model)
	}
	if options.Profile != "" {
		args = append(args, "--profile", options.Profile)
	}
	if options.ReasoningEffort != "" {
		args = append(args, "--reasoning-effort", options.ReasoningEffort)
	}
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, options.Binary, args...)
	if options.WorkingDirectory != "" {
		cmd.Dir = options.WorkingDirectory
	}
	if options.DataDir != "" {
		cmd.Env = environmentWith("CAKE_DATA_DIR", options.DataDir)
	}
	cmd.Stdin = strings.NewReader(options.Instructions)
	label := "read-only resume"
	if reuse {
		label = "request reuse"
	}
	return runCompletion(ctx, cmd, label)
}

func writeOutputSchema(dir, schema string) (string, error) {
	path := filepath.Join(dir, "output-schema.json")
	if err := os.WriteFile(path, []byte(schema), 0o600); err != nil {
		return "", fmt.Errorf("write output schema: %w", err)
	}
	return path, nil
}

// runCompletion executes one Cake completion and decodes its one-document
// JSON result. label names the call in diagnostics.
func runCompletion(ctx context.Context, cmd *exec.Cmd, label string) (string, Usage, error) {
	var usage Usage
	stdout := &capBuffer{limit: maxCompletionBytes}
	stderr := &tailBuffer{limit: maxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return "", usage, completionError(ctx, label, err, stderr.String(), stdout.String())
	}
	if stdout.overflow {
		return "", usage, fmt.Errorf("%s exceeded %d bytes", label, maxCompletionBytes)
	}

	var completion Completion
	if err := json.Unmarshal(stdout.Bytes(), &completion); err != nil {
		return "", usage, fmt.Errorf(
			"%s returned invalid completion JSON: %w: %s",
			label, err, strings.TrimSpace(stdout.String()),
		)
	}
	if len(completion.Usage) > 0 {
		// Usage is diagnostic: an unfamiliar shape must not fail a suggestion
		// that otherwise succeeded.
		_ = json.Unmarshal(completion.Usage, &usage)
	}
	if completion.Error != nil && strings.TrimSpace(*completion.Error) != "" {
		return "", usage, fmt.Errorf("%s failed: %s", label, strings.TrimSpace(*completion.Error))
	}
	if completion.Result == nil || strings.TrimSpace(*completion.Result) == "" {
		return "", usage, fmt.Errorf("%s returned an empty result", label)
	}
	return strings.TrimSpace(*completion.Result), usage, nil
}

func completionError(ctx context.Context, label string, err error, stderr, stdout string) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("%s timed out: %w", label, ctx.Err())
	case ctx.Err() != nil:
		return fmt.Errorf("%s canceled: %w", label, ctx.Err())
	}
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = strings.TrimSpace(stdout)
	}
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("%s failed: %s", label, detail)
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

// environmentWith returns the process environment with the supplied key/value
// pairs replaced, so the child sees the intended values.
func environmentWith(keyValues ...string) []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env)+len(keyValues)/2)
	for _, entry := range env {
		skip := false
		for i := 0; i+1 < len(keyValues); i += 2 {
			if strings.HasPrefix(entry, keyValues[i]+"=") {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, entry)
		}
	}
	for i := 0; i+1 < len(keyValues); i += 2 {
		filtered = append(filtered, keyValues[i]+"="+keyValues[i+1])
	}
	return filtered
}

var _ io.Writer = (*capBuffer)(nil)
var _ io.Writer = (*tailBuffer)(nil)
