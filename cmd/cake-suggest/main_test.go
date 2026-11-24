//go:build !windows

package main

import (
	"crypto/sha256"
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
	testTaskID = "11111111-1111-4111-8111-111111111111"
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
		return fakeReplay(args)
	}
	if hasArg(args, "--reuse-last-request") {
		return fakeReuse(args)
	}
	fmt.Fprintln(os.Stderr, "fake cake: unexpected invocation")
	return 2
}

func fakeReplay(args []string) int {
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

func fakeReuse(args []string) int {
	if path := os.Getenv("FAKE_REUSE_ARGS_FILE"); path != "" {
		writeFile(path, strings.Join(args, "\n"))
	}
	if path := os.Getenv("FAKE_REUSE_STDIN_FILE"); path != "" {
		body, _ := io.ReadAll(os.Stdin)
		writeFile(path, string(body))
	} else {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	if path := os.Getenv("FAKE_REUSE_CWD_FILE"); path != "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "fake cake: getwd:", err)
			return 1
		}
		writeFile(path, cwd)
	}
	if path := os.Getenv("FAKE_REUSE_SCHEMA_FILE"); path != "" {
		copyFlagFile(args, "--output-schema", path)
	}
	if path := os.Getenv("FAKE_REUSE_DATA_DIR_FILE"); path != "" {
		writeFile(path, os.Getenv("CAKE_DATA_DIR"))
	}
	if raw := os.Getenv("FAKE_REUSE_EXIT"); raw != "" {
		code, _ := strconv.Atoi(raw)
		fmt.Fprint(os.Stderr, os.Getenv("FAKE_REUSE_STDERR"))
		return code
	}
	if completion := os.Getenv("FAKE_REUSE_COMPLETION"); completion != "" {
		fmt.Print(completion)
		return 0
	}
	return emitCompletion(os.Getenv("FAKE_SUGGEST_DOCUMENT"))
}

func emitCompletion(result string) int {
	usage := map[string]any{
		"input_tokens":  123,
		"output_tokens": 45,
		"total_tokens":  168,
		"input_tokens_details": map[string]any{
			"cached_tokens":      0,
			"cache_write_tokens": 0,
		},
	}
	if raw := os.Getenv("FAKE_USAGE_JSON"); raw != "" {
		var custom map[string]any
		if err := json.Unmarshal([]byte(raw), &custom); err == nil {
			usage = custom
		}
	}
	encoded, err := json.Marshal(map[string]any{
		"result": result,
		"usage":  usage,
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

func replayFixture(workingDir string) string {
	return strings.Join([]string{
		sessionMeta(workingDir),
		`{"type":"task_start","session_id":"` + testID + `","task_id":"` + testTaskID + `"}`,
		`{"type":"message","role":"user","content":"Add a retry test."}`,
		`{"type":"message","role":"assistant","content":"I added the test and it passes."}`,
	}, "\n") + "\n"
}

func sessionMeta(workingDir string) string {
	fields := map[string]any{
		"type":           "session_meta",
		"format_version": 4,
		"session_id":     testID,
		"model":          "test-model",
		"model_config":   "test-config",
	}
	if workingDir != "" {
		fields["working_directory"] = workingDir
	}
	encoded, _ := json.Marshal(fields)
	return string(encoded)
}

func writeCapture(t *testing.T, cacheDir string, mutate func(map[string]any)) {
	t.Helper()
	body := `{"messages":[{"role":"system","content":"captured"}]}`
	hash := sha256.Sum256([]byte(body))
	capture := map[string]any{
		"version":    float64(1),
		"session_id": testID,
		"task_id":    testTaskID,
		"profile":    nil,
		"provider_identity": map[string]any{
			"model":        "test-model",
			"model_config": "test-config",
		},
		"backend":     "chat_completions",
		"body":        body,
		"body_sha256": fmt.Sprintf("%x", hash),
		"body_len":    float64(len(body)),
	}
	if mutate != nil {
		mutate(capture)
	}
	encoded, err := json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cacheDir, "session-requests")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, testID+".json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
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

func TestSuggestUsesRequestReuse(t *testing.T) {
	project := t.TempDir()
	fake := writeFakeCake(t)
	temp := t.TempDir()
	reuseArgs := filepath.Join(temp, "reuse-args")
	reuseStdin := filepath.Join(temp, "reuse-stdin")
	reuseCWD := filepath.Join(temp, "reuse-cwd")
	reuseSchema := filepath.Join(temp, "reuse-schema")
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(project)))
	writeCapture(t, temp, func(capture map[string]any) {
		capture["profile"] = "concise"
	})
	t.Setenv("FAKE_REUSE_ARGS_FILE", reuseArgs)
	t.Setenv("FAKE_REUSE_STDIN_FILE", reuseStdin)
	t.Setenv("FAKE_REUSE_CWD_FILE", reuseCWD)
	t.Setenv("FAKE_REUSE_SCHEMA_FILE", reuseSchema)
	t.Setenv("FAKE_USAGE_JSON", `{"input_tokens":500,"input_tokens_details":{"cached_tokens":321,"cache_write_tokens":12},"output_tokens":50,"total_tokens":550}`)
	documentEnv(t, map[string]any{
		"suggested":  true,
		"suggestion": "Add a test for the retry path.",
	})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{
			"suggest", "-json", "-verbose",
			"-cake-bin", fake,
			"-data-dir", temp,
			"-model", "test-config",
			"-profile", "concise",
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

	args := readFile(t, reuseArgs)
	for _, want := range []string{
		"--resume\n" + testID,
		"--reuse-last-request",
		"--output-format\njson",
		"--output-schema",
		"--model\ntest-config",
		"--profile\nconcise",
		"\n-",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("reuse args do not contain %q:\n%s", want, args)
		}
	}
	for _, forbidden := range []string{
		"--read-only", "--tool-choice", "--reasoning-effort",
		"--no-session", "--no-tools", "--no-skills", "--system-prompt",
	} {
		if strings.Contains(args, forbidden) {
			t.Errorf("reuse args must not contain %q:\n%s", forbidden, args)
		}
	}
	if cwd := readFile(t, reuseCWD); !samePath(t, cwd, project) {
		t.Errorf("reuse cwd = %q, want %q", cwd, project)
	}
	if stdin := readFile(t, reuseStdin); !strings.Contains(stdin, "END OF SUGGESTION REQUEST") ||
		!strings.Contains(stdin, "$schema") {
		t.Errorf("reuse stdin missing the suggestion request or output schema:\n%s", stdin)
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(readFile(t, reuseSchema)), &schema); err != nil {
		t.Fatalf("copied output schema is invalid: %v", err)
	}
	if !strings.Contains(stderr, "mode=reuse") {
		t.Errorf("verbose output does not name the reuse mode: %s", stderr)
	}
	if !strings.Contains(stderr, "cached_tokens=321") || !strings.Contains(stderr, "cache_write_tokens=12") {
		t.Errorf("verbose output does not report cached tokens: %s", stderr)
	}
}

func TestSuggestFindsTheDefaultCacheCapture(t *testing.T) {
	project := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CAKE_DATA_DIR", "")
	fake := writeFakeCake(t)
	temp := t.TempDir()
	reuseArgs := filepath.Join(temp, "reuse-args")
	reuseDataDir := filepath.Join(temp, "reuse-data-dir")
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(project)))
	writeCapture(t, filepath.Join(home, ".cache", "cake"), nil)
	t.Setenv("FAKE_REUSE_ARGS_FILE", reuseArgs)
	t.Setenv("FAKE_REUSE_DATA_DIR_FILE", reuseDataDir)
	documentEnv(t, map[string]any{
		"suggested":  true,
		"suggestion": "Add a test for the retry path.",
	})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-verbose", "-cake-bin", fake, "--", testID})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	var doc suggest.Document
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Suggested {
		t.Fatalf("unexpected document: %+v", doc)
	}
	if _, err := os.Stat(reuseArgs); err != nil {
		t.Fatalf("reuse did not run from the default cache capture: %v", err)
	}
	if dataDir := readFile(t, reuseDataDir); dataDir != "" {
		t.Fatalf("child CAKE_DATA_DIR = %q, want empty for Cake's default split", dataDir)
	}
	if !strings.Contains(stderr, "mode=reuse") {
		t.Errorf("verbose output does not name the reuse mode: %s", stderr)
	}
}

func TestSuggestNoOpsWhenTheCaptureIsUnusable(t *testing.T) {
	tests := []struct {
		name      string
		noCapture bool
		mutate    func(map[string]any)
		args      []string
		want      string
	}{
		{name: "missing", noCapture: true, want: "capture_unavailable"},
		{
			name: "stale",
			mutate: func(capture map[string]any) {
				capture["task_id"] = "22222222-2222-4222-8222-222222222222"
			},
			want: "capture_stale",
		},
		{
			name: "profile",
			mutate: func(capture map[string]any) {
				capture["profile"] = "other"
			},
			want: "capture_incompatible",
		},
		{
			name: "digest",
			mutate: func(capture map[string]any) {
				capture["body_sha256"] = "bad"
			},
			want: "capture_incompatible",
		},
		{
			name: "model",
			args: []string{"-model", "other-model"},
			want: "capture_incompatible",
		},
		{
			name: "reasoning effort",
			args: []string{"-reasoning-effort", "high"},
			want: "cannot be applied",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := writeFakeCake(t)
			temp := t.TempDir()
			reuseArgs := filepath.Join(temp, "reuse-args")
			t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(t.TempDir())))
			if !test.noCapture {
				writeCapture(t, temp, test.mutate)
			}
			t.Setenv("FAKE_REUSE_ARGS_FILE", reuseArgs)
			documentEnv(t, map[string]any{
				"suggested":  true,
				"suggestion": "Add a test for the retry path.",
			})

			args := append([]string{"suggest", "-json", "-verbose", "-cake-bin", fake, "-data-dir", temp}, test.args...)
			args = append(args, "--", testID)
			code, stdout, stderr := captureOutput(t, func() int {
				return runCLI(args)
			})
			if code != 0 {
				t.Fatalf("exit code = %d, stderr: %s", code, stderr)
			}
			var doc suggest.Document
			if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Suggested || doc.SessionID != testID || !strings.Contains(doc.Reason, test.want) {
				t.Fatalf("unexpected document: %+v, want reason %q", doc, test.want)
			}
			if _, err := os.Stat(reuseArgs); !os.IsNotExist(err) {
				t.Fatalf("reuse ran with an unusable capture: %v", err)
			}
			if !strings.Contains(stderr, "mode=none") || !strings.Contains(stderr, test.want) {
				t.Errorf("verbose output = %s, want mode=none and %s", stderr, test.want)
			}
		})
	}
}

func TestSuggestReuseFailureExitsNonzero(t *testing.T) {
	fake := writeFakeCake(t)
	temp := t.TempDir()
	reuseArgs := filepath.Join(temp, "reuse-args")
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(t.TempDir())))
	writeCapture(t, temp, nil)
	t.Setenv("FAKE_REUSE_ARGS_FILE", reuseArgs)
	t.Setenv("FAKE_REUSE_EXIT", "1")
	t.Setenv("FAKE_REUSE_STDERR", "request capture stale")

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-verbose", "-cake-bin", fake, "-data-dir", temp, "--", testID})
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("failure wrote stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "request reuse failed") || !strings.Contains(stderr, "request capture stale") {
		t.Fatalf("stderr = %s", stderr)
	}
	if _, err := os.Stat(reuseArgs); err != nil {
		t.Fatalf("reuse was not attempted: %v", err)
	}
}

func TestSuggestNoOp(t *testing.T) {
	fake := writeFakeCake(t)
	temp := t.TempDir()
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(t.TempDir())))
	writeCapture(t, temp, nil)
	argsPath := filepath.Join(t.TempDir(), "reuse-args")
	t.Setenv("FAKE_REUSE_ARGS_FILE", argsPath)
	documentEnv(t, map[string]any{
		"suggested": false,
		"reason":    "The work is complete.",
	})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-cake-bin", fake, "-data-dir", temp, "--", testID})
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

func TestSuggestRejectsInvalidModelDocument(t *testing.T) {
	fake := writeFakeCake(t)
	temp := t.TempDir()
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(t.TempDir())))
	writeCapture(t, temp, nil)
	documentEnv(t, map[string]any{"suggestion": "Add a test."})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-cake-bin", fake, "-data-dir", temp, "--", testID})
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
	temp := t.TempDir()
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(t.TempDir())))
	writeCapture(t, temp, nil)
	documentEnv(t, map[string]any{
		"suggested":  true,
		"suggestion": "  \"Add\n a test.\"  ",
	})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-cake-bin", fake, "-data-dir", temp, "--", testID})
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

func samePath(t *testing.T, got, want string) bool {
	t.Helper()
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	wantResolved, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}
	return gotResolved == wantResolved
}
