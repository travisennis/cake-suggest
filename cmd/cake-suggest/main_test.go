//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/travisennis/cake-suggest/internal/suggest"
)

const (
	fakeEnable = "CAKE_SUGGEST_FAKE"
	testID     = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
)

// TestFakeCakeHelper stands in for Cake so the CLI tests cross a real process
// boundary without a model call or a real Cake installation.
func TestFakeCakeHelper(t *testing.T) {
	if os.Getenv(fakeEnable) != "1" {
		return
	}
	os.Exit(fakeCakeMain(cakeArgs(os.Args[1:])))
}

func cakeArgs(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return args
}

func fakeCakeMain(args []string) int {
	if hasArg(args, "replay") {
		if path := os.Getenv("FAKE_REPLAY_ARGS_FILE"); path != "" {
			writeFile(path, strings.Join(args, "\n"))
		}
		if raw := os.Getenv("FAKE_REPLAY_EXIT"); raw != "" {
			code, _ := strconv.Atoi(raw)
			fmt.Fprint(os.Stderr, os.Getenv("FAKE_REPLAY_STDERR"))
			return code
		}
		data, err := os.ReadFile(os.Getenv("FAKE_REPLAY_FILE"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "fake cake: read replay:", err)
			return 1
		}
		_, _ = os.Stdout.Write(data)
		return 0
	}

	if path := os.Getenv("FAKE_ARGS_FILE"); path != "" {
		writeFile(path, strings.Join(args, "\n"))
	}
	if path := os.Getenv("FAKE_STDIN_FILE"); path != "" {
		body, _ := io.ReadAll(os.Stdin)
		writeFile(path, string(body))
	} else {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	if path := os.Getenv("FAKE_PROMPT_FILE"); path != "" {
		copyFlagFile(args, "--system-prompt", path)
	}
	if path := os.Getenv("FAKE_SCHEMA_FILE"); path != "" {
		copyFlagFile(args, "--output-schema", path)
	}
	if raw := os.Getenv("FAKE_MODEL_EXIT"); raw != "" {
		code, _ := strconv.Atoi(raw)
		fmt.Fprint(os.Stderr, os.Getenv("FAKE_MODEL_STDERR"))
		return code
	}
	if completion := os.Getenv("FAKE_COMPLETION"); completion != "" {
		fmt.Print(completion)
		return 0
	}
	result := os.Getenv("FAKE_SUGGEST_DOCUMENT")
	encoded, err := json.Marshal(map[string]any{
		"result": result,
		"usage": map[string]any{
			"input_tokens":  123,
			"output_tokens": 45,
			"total_tokens":  168,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake cake: encode completion:", err)
		return 1
	}
	fmt.Println(string(encoded))
	return 0
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func copyFlagFile(args []string, flag, dest string) {
	for i, arg := range args {
		if arg != flag || i+1 >= len(args) {
			continue
		}
		data, err := os.ReadFile(args[i+1])
		if err != nil {
			return
		}
		writeFile(dest, string(data))
		return
	}
}

func writeFile(path, content string) {
	_ = os.WriteFile(path, []byte(content), 0o600)
}

func writeFakeCake(t *testing.T) string {
	t.Helper()
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fake-cake")
	script := fmt.Sprintf("#!/bin/sh\nexec '%s' -test.run='^TestFakeCakeHelper$' -- \"$@\"\n", binary)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeEnable, "1")
	return path
}

func captureOutput(t *testing.T, run func() int) (int, string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = outWrite, errWrite

	type captured struct{ stdout, stderr string }
	done := make(chan captured, 1)
	go func() {
		out, _ := io.ReadAll(outRead)
		errOut, _ := io.ReadAll(errRead)
		done <- captured{string(out), string(errOut)}
	}()

	code := run()

	_ = outWrite.Close()
	_ = errWrite.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	result := <-done
	_ = outRead.Close()
	_ = errRead.Close()
	return code, result.stdout, result.stderr
}

func replayFixture(lines ...string) string {
	base := []string{
		`{"type":"session_meta","format_version":4,"session_id":"` + testID + `","working_directory":"/tmp/project","model":"test-model"}`,
		`{"type":"message","role":"user","content":"Add a retry test."}`,
		`{"type":"message","role":"assistant","content":"I added the test and it passes."}`,
		`{"type":"function_call","name":"Bash","call_id":"call-1","arguments":"{\"command\":\"go test ./...\"}"}`,
		`{"type":"function_call_output","call_id":"call-1","output":"ok example.test"}`,
	}
	return strings.Join(append(base, lines...), "\n") + "\n"
}

func writeReplay(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "replay.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func documentEnv(t *testing.T, document map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_SUGGEST_DOCUMENT", string(encoded))
}

func TestSuggestJSONUsesReplayAndModel(t *testing.T) {
	fake := writeFakeCake(t)
	replay := writeReplay(t, replayFixture())
	temp := t.TempDir()
	argsPath := filepath.Join(temp, "args")
	stdinPath := filepath.Join(temp, "stdin")
	promptPath := filepath.Join(temp, "prompt")
	schemaPath := filepath.Join(temp, "schema")
	t.Setenv("FAKE_REPLAY_FILE", replay)
	t.Setenv("FAKE_ARGS_FILE", argsPath)
	t.Setenv("FAKE_STDIN_FILE", stdinPath)
	t.Setenv("FAKE_PROMPT_FILE", promptPath)
	t.Setenv("FAKE_SCHEMA_FILE", schemaPath)
	documentEnv(t, map[string]any{
		"suggested":  true,
		"suggestion": "Add a test for the retry path.",
	})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{
			"suggest",
			"-json",
			"-verbose",
			"-cake-bin", fake,
			"-data-dir", temp,
			"-model", "test-model",
			"-profile", "concise",
			"-reasoning-effort", "high",
			"--", testID,
		})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}

	var doc suggest.Document
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("invalid result JSON: %v\n%s", err, stdout)
	}
	if !doc.Suggested || doc.Suggestion != "Add a test for the retry path." || doc.SessionID != testID {
		t.Fatalf("unexpected document: %+v", doc)
	}

	args := readFile(t, argsPath)
	for _, want := range []string{
		"--no-session", "--no-tools", "--no-skills",
		"--output-format\njson", "--system-prompt", "--output-schema",
		"--model\ntest-model", "--profile\nconcise",
		"--reasoning-effort\nhigh", "\n-",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("model args do not contain %q:\n%s", want, args)
		}
	}
	if strings.Contains(args, "--resume") {
		t.Error("model args must not resume the session")
	}

	stdin := readFile(t, stdinPath)
	for _, want := range []string{"USER:\nAdd a retry test.", "ASSISTANT:", "TOOL RESULT:", "END OF TRANSCRIPT"} {
		if !strings.Contains(stdin, want) {
			t.Errorf("model stdin does not contain %q:\n%s", want, stdin)
		}
	}
	if strings.Contains(stdin, "private reasoning") {
		t.Error("model stdin contains reasoning content")
	}

	prompt := readFile(t, promptPath)
	if !strings.Contains(strings.ToLower(prompt), "untrusted") {
		t.Errorf("system prompt does not call the transcript untrusted:\n%s", prompt)
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(readFile(t, schemaPath)), &schema); err != nil {
		t.Fatalf("copied output schema is invalid: %v", err)
	}
	if !strings.Contains(stderr, "entries=") {
		t.Errorf("verbose output missing timings: %s", stderr)
	}
	if strings.Contains(stderr, "Add a retry test.") {
		t.Errorf("verbose output leaked transcript content: %s", stderr)
	}
}

func TestSuggestNoOp(t *testing.T) {
	fake := writeFakeCake(t)
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture()))
	argsPath := filepath.Join(t.TempDir(), "model-args")
	t.Setenv("FAKE_ARGS_FILE", argsPath)
	documentEnv(t, map[string]any{
		"suggested": false,
		"reason":    "The work is complete.",
	})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-cake-bin", fake, "--", testID})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	var doc suggest.Document
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Suggested || doc.Reason != "The work is complete." || doc.SessionID != testID {
		t.Fatalf("unexpected document: %+v", doc)
	}
	if args := readFile(t, argsPath); strings.Contains(args, "--reasoning-effort") {
		t.Errorf("empty reasoning effort emitted a flag:\n%s", args)
	}
}

func TestSuggestNoConversationSkipsTheModel(t *testing.T) {
	fake := writeFakeCake(t)
	replay := strings.Join([]string{
		`{"type":"session_meta","format_version":4,"session_id":"` + testID + `"}`,
		`{"type":"turn_usage","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
	}, "\n") + "\n"
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replay))
	argsPath := filepath.Join(t.TempDir(), "model-args")
	t.Setenv("FAKE_ARGS_FILE", argsPath)

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-cake-bin", fake, "--", testID})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	var doc suggest.Document
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Suggested || !strings.Contains(doc.Reason, "no user or assistant") {
		t.Fatalf("unexpected document: %+v", doc)
	}
	if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
		t.Fatalf("model was invoked for an empty transcript: %v", err)
	}
}

func TestSuggestRejectsInvalidModelDocument(t *testing.T) {
	fake := writeFakeCake(t)
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture()))
	documentEnv(t, map[string]any{"suggestion": "Add a test."})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-cake-bin", fake, "--", testID})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout: %s; stderr: %s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("failure wrote stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "suggested field") {
		t.Fatalf("stderr = %s, want a missing suggested diagnostic", stderr)
	}
}

func TestSuggestRejectsReplayFailure(t *testing.T) {
	fake := writeFakeCake(t)
	t.Setenv("FAKE_REPLAY_EXIT", "1")
	t.Setenv("FAKE_REPLAY_STDERR", "session not found")

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-cake-bin", fake, "--", testID})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout: %s; stderr: %s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("failure wrote stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "cake replay failed") || !strings.Contains(stderr, "session not found") {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSuggestSanitizesTheModelText(t *testing.T) {
	fake := writeFakeCake(t)
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture()))
	documentEnv(t, map[string]any{
		"suggested":  true,
		"suggestion": "  \"Add\n a test.\"  ",
	})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-cake-bin", fake, "--", testID})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	var doc suggest.Document
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Suggestion != "Add a test." {
		t.Fatalf("suggestion = %q, want %q", doc.Suggestion, "Add a test.")
	}
}

func TestSuggestRequiresOneValidSessionID(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing", args: []string{"suggest", "-json"}, want: "exactly one session id"},
		{name: "invalid", args: []string{"suggest", "-json", "--", "not-a-uuid"}, want: "invalid session id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := captureOutput(t, func() int {
				return runCLI(test.args)
			})
			if code != 2 {
				t.Fatalf("exit code = %d, want 2", code)
			}
			if stdout != "" {
				t.Fatalf("usage failure wrote stdout: %q", stdout)
			}
			if !strings.Contains(stderr, test.want) {
				t.Fatalf("stderr = %s, want %q", stderr, test.want)
			}
		})
	}
}

func TestUsageAndVersion(t *testing.T) {
	for _, arg := range []string{"version", "-version", "-V", "--version"} {
		t.Run(arg, func(t *testing.T) {
			code, stdout, stderr := captureOutput(t, func() int { return runCLI([]string{arg}) })
			if code != 0 || !strings.HasPrefix(stdout, "cake-suggest 0.1.0") {
				t.Fatalf("version: code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if stderr != "" {
				t.Fatalf("version wrote stderr: %q", stderr)
			}
		})
	}
	code, stdout, stderr := captureOutput(t, func() int { return runCLI([]string{"help"}) })
	if code != 0 || stdout != "" || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("help: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
