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
	if hasArg(args, "--resume") {
		return fakeResumedCall(args)
	}
	return fakeExcerpt(args)
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

func fakeResumedCall(args []string) int {
	prefix := "FAKE_RESUME_"
	if hasArg(args, "--reuse-last-request") {
		prefix = "FAKE_REUSE_"
	}
	if path := os.Getenv(prefix + "ARGS_FILE"); path != "" {
		writeFile(path, strings.Join(args, "\n"))
	}
	if path := os.Getenv(prefix + "STDIN_FILE"); path != "" {
		body, _ := io.ReadAll(os.Stdin)
		writeFile(path, string(body))
	} else {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	if path := os.Getenv(prefix + "CWD_FILE"); path != "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "fake cake: getwd:", err)
			return 1
		}
		writeFile(path, cwd)
	}
	if path := os.Getenv(prefix + "SCHEMA_FILE"); path != "" {
		copyFlagFile(args, "--output-schema", path)
	}
	if path := os.Getenv(prefix + "DATA_DIR_FILE"); path != "" {
		writeFile(path, os.Getenv("CAKE_DATA_DIR"))
	}
	if raw := os.Getenv(prefix + "EXIT"); raw != "" {
		code, _ := strconv.Atoi(raw)
		fmt.Fprint(os.Stderr, os.Getenv(prefix+"STDERR"))
		return code
	}
	if completion := os.Getenv(prefix + "COMPLETION"); completion != "" {
		fmt.Print(completion)
		return 0
	}
	document := os.Getenv(prefix + "DOCUMENT")
	if document == "" {
		document = os.Getenv("FAKE_SUGGEST_DOCUMENT")
	}
	return emitCompletion(document)
}

func fakeExcerpt(args []string) int {
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

func replayFixture(workingDir string, lines ...string) string {
	return replayWithMeta(sessionMeta(workingDir, "test-config"), lines...)
}

func sessionMeta(workingDir, modelConfig string) string {
	fields := map[string]any{
		"type":           "session_meta",
		"format_version": 4,
		"session_id":     testID,
		"model":          "test-model",
	}
	if workingDir != "" {
		fields["working_directory"] = workingDir
	}
	if modelConfig != "" {
		fields["model_config"] = modelConfig
	}
	encoded, _ := json.Marshal(fields)
	return string(encoded)
}

func replayWithMeta(meta string, lines ...string) string {
	base := []string{
		meta,
		`{"type":"task_start","session_id":"` + testID + `","task_id":"` + testTaskID + `","timestamp":"2026-11-23T10:00:00Z","model":"test-model","model_config":"test-config"}`,
		`{"type":"message","role":"user","content":"Add a retry test."}`,
		`{"type":"message","role":"assistant","content":"I added the test and it passes."}`,
		`{"type":"function_call","name":"Bash","call_id":"call-1","arguments":"{\"command\":\"go test ./...\"}"}`,
		`{"type":"function_call_output","call_id":"call-1","output":"ok example.test"}`,
	}
	return strings.Join(append(base, lines...), "\n") + "\n"
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

func TestSuggestUsesReadOnlyResume(t *testing.T) {
	project := t.TempDir()
	fake := writeFakeCake(t)
	temp := t.TempDir()
	resumeArgs := filepath.Join(temp, "resume-args")
	resumeStdin := filepath.Join(temp, "resume-stdin")
	resumeCWD := filepath.Join(temp, "resume-cwd")
	resumeSchema := filepath.Join(temp, "resume-schema")
	excerptArgs := filepath.Join(temp, "excerpt-args")
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(project)))
	t.Setenv("FAKE_RESUME_ARGS_FILE", resumeArgs)
	t.Setenv("FAKE_RESUME_STDIN_FILE", resumeStdin)
	t.Setenv("FAKE_RESUME_CWD_FILE", resumeCWD)
	t.Setenv("FAKE_RESUME_SCHEMA_FILE", resumeSchema)
	t.Setenv("FAKE_ARGS_FILE", excerptArgs)
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
			"-model", "test-config",
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

	args := readFile(t, resumeArgs)
	for _, want := range []string{
		"--resume\n" + testID,
		"--read-only",
		"--tool-choice\nnone",
		"--output-format\njson",
		"--output-schema",
		"--model\ntest-config",
		"--profile\nconcise",
		"--reasoning-effort\nhigh",
		"\n-",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("resume args do not contain %q:\n%s", want, args)
		}
	}
	for _, forbidden := range []string{"--no-session", "--no-tools", "--no-skills", "--system-prompt"} {
		if strings.Contains(args, forbidden) {
			t.Errorf("resume args must not contain %q:\n%s", forbidden, args)
		}
	}
	if cwd := readFile(t, resumeCWD); !samePath(t, cwd, project) {
		t.Errorf("resume cwd = %q, want %q", cwd, project)
	}

	stdin := readFile(t, resumeStdin)
	for _, want := range []string{
		"cake-suggest",
		"untrusted",
		"Return only the JSON document",
		"END OF SUGGESTION REQUEST",
	} {
		if !strings.Contains(stdin, want) {
			t.Errorf("resume stdin does not contain %q:\n%s", want, stdin)
		}
	}
	if strings.Contains(stdin, "USER:\nAdd a retry test.") {
		t.Error("resume stdin contains the fallback transcript excerpt")
	}
	if _, err := os.Stat(excerptArgs); !os.IsNotExist(err) {
		t.Fatalf("excerpt fallback ran for a successful resume: %v", err)
	}

	var schema map[string]any
	if err := json.Unmarshal([]byte(readFile(t, resumeSchema)), &schema); err != nil {
		t.Fatalf("copied output schema is invalid: %v", err)
	}
	if !strings.Contains(stderr, "mode=resume") {
		t.Errorf("verbose output does not name the resume mode: %s", stderr)
	}
	if strings.Contains(stderr, "Add a retry test.") {
		t.Errorf("verbose output leaked transcript content: %s", stderr)
	}
}

func TestSuggestUsesRequestReuse(t *testing.T) {
	project := t.TempDir()
	fake := writeFakeCake(t)
	temp := t.TempDir()
	reuseArgs := filepath.Join(temp, "reuse-args")
	reuseStdin := filepath.Join(temp, "reuse-stdin")
	reuseCWD := filepath.Join(temp, "reuse-cwd")
	reuseSchema := filepath.Join(temp, "reuse-schema")
	resumeArgs := filepath.Join(temp, "resume-args")
	excerptArgs := filepath.Join(temp, "excerpt-args")
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(project)))
	writeCapture(t, temp, func(capture map[string]any) {
		capture["profile"] = "concise"
	})
	t.Setenv("FAKE_REUSE_ARGS_FILE", reuseArgs)
	t.Setenv("FAKE_REUSE_STDIN_FILE", reuseStdin)
	t.Setenv("FAKE_REUSE_CWD_FILE", reuseCWD)
	t.Setenv("FAKE_REUSE_SCHEMA_FILE", reuseSchema)
	t.Setenv("FAKE_RESUME_ARGS_FILE", resumeArgs)
	t.Setenv("FAKE_ARGS_FILE", excerptArgs)
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
		"--tool-choice\nnone",
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
	for _, forbidden := range []string{"--read-only", "--no-session", "--no-tools", "--no-skills", "--system-prompt"} {
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
	for name, path := range map[string]string{"resume": resumeArgs, "excerpt": excerptArgs} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s ran after successful reuse: %v", name, err)
		}
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
	if strings.Contains(stderr, "Add a retry test.") {
		t.Errorf("verbose output leaked transcript content: %s", stderr)
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

func TestSuggestFallsBackToReadOnlyResume(t *testing.T) {
	project := t.TempDir()
	fake := writeFakeCake(t)
	temp := t.TempDir()
	reuseArgs := filepath.Join(temp, "reuse-args")
	resumeArgs := filepath.Join(temp, "resume-args")
	excerptArgs := filepath.Join(temp, "excerpt-args")
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(project)))
	t.Setenv("FAKE_REUSE_ARGS_FILE", reuseArgs)
	t.Setenv("FAKE_RESUME_ARGS_FILE", resumeArgs)
	t.Setenv("FAKE_ARGS_FILE", excerptArgs)
	documentEnv(t, map[string]any{
		"suggested":  true,
		"suggestion": "Add a test for the retry path.",
	})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-verbose", "-cake-bin", fake, "-data-dir", temp, "--", testID})
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
	if _, err := os.Stat(reuseArgs); !os.IsNotExist(err) {
		t.Fatalf("reuse ran without a capture: %v", err)
	}
	if args := readFile(t, resumeArgs); !strings.Contains(args, "--read-only") {
		t.Errorf("resume was not attempted:\n%s", args)
	}
	if _, err := os.Stat(excerptArgs); !os.IsNotExist(err) {
		t.Fatalf("excerpt ran after a successful resume: %v", err)
	}
	if !strings.Contains(stderr, "mode=resume") || !strings.Contains(stderr, "capture_unavailable") {
		t.Errorf("verbose output = %s, want mode=resume and capture_unavailable", stderr)
	}
}

func TestSuggestRejectsUnusableCaptures(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
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
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := t.TempDir()
			fake := writeFakeCake(t)
			temp := t.TempDir()
			reuseArgs := filepath.Join(temp, "reuse-args")
			resumeArgs := filepath.Join(temp, "resume-args")
			excerptArgs := filepath.Join(temp, "excerpt-args")
			t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(project)))
			writeCapture(t, temp, test.mutate)
			t.Setenv("FAKE_REUSE_ARGS_FILE", reuseArgs)
			t.Setenv("FAKE_RESUME_ARGS_FILE", resumeArgs)
			t.Setenv("FAKE_ARGS_FILE", excerptArgs)
			documentEnv(t, map[string]any{
				"suggested":  true,
				"suggestion": "Add a test for the retry path.",
			})

			code, stdout, stderr := captureOutput(t, func() int {
				return runCLI([]string{"suggest", "-json", "-verbose", "-cake-bin", fake, "-data-dir", temp, "--", testID})
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
			if _, err := os.Stat(reuseArgs); !os.IsNotExist(err) {
				t.Fatalf("reuse ran with an unusable capture: %v", err)
			}
			if _, err := os.Stat(resumeArgs); err != nil {
				t.Fatalf("resume did not run: %v", err)
			}
			if _, err := os.Stat(excerptArgs); !os.IsNotExist(err) {
				t.Fatalf("excerpt ran after a successful resume: %v", err)
			}
			if !strings.Contains(stderr, "mode=resume") || !strings.Contains(stderr, test.want) {
				t.Errorf("verbose output = %s, want mode=resume and %s", stderr, test.want)
			}
		})
	}
}

func TestSuggestReuseCallFailureFallsBackToResume(t *testing.T) {
	project := t.TempDir()
	fake := writeFakeCake(t)
	temp := t.TempDir()
	reuseArgs := filepath.Join(temp, "reuse-args")
	resumeArgs := filepath.Join(temp, "resume-args")
	excerptArgs := filepath.Join(temp, "excerpt-args")
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(project)))
	writeCapture(t, temp, nil)
	t.Setenv("FAKE_REUSE_ARGS_FILE", reuseArgs)
	t.Setenv("FAKE_REUSE_EXIT", "1")
	t.Setenv("FAKE_REUSE_STDERR", "request capture stale")
	t.Setenv("FAKE_RESUME_ARGS_FILE", resumeArgs)
	t.Setenv("FAKE_ARGS_FILE", excerptArgs)
	documentEnv(t, map[string]any{
		"suggested":  true,
		"suggestion": "Add a test for the retry path.",
	})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-verbose", "-cake-bin", fake, "-data-dir", temp, "--", testID})
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
		t.Fatalf("reuse was not attempted: %v", err)
	}
	if _, err := os.Stat(resumeArgs); err != nil {
		t.Fatalf("resume did not run after reuse failed: %v", err)
	}
	if _, err := os.Stat(excerptArgs); !os.IsNotExist(err) {
		t.Fatalf("excerpt ran after a successful resume: %v", err)
	}
	if !strings.Contains(stderr, "mode=resume") ||
		!strings.Contains(stderr, "request reuse failed") ||
		!strings.Contains(stderr, "request capture stale") {
		t.Errorf("verbose output = %s, want the reuse failure and resume mode", stderr)
	}
}

func TestSuggestReuseNoOpDoesNotFallBack(t *testing.T) {
	project := t.TempDir()
	fake := writeFakeCake(t)
	temp := t.TempDir()
	resumeArgs := filepath.Join(temp, "resume-args")
	excerptArgs := filepath.Join(temp, "excerpt-args")
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(project)))
	writeCapture(t, temp, nil)
	t.Setenv("FAKE_RESUME_ARGS_FILE", resumeArgs)
	t.Setenv("FAKE_ARGS_FILE", excerptArgs)
	documentEnv(t, map[string]any{
		"suggested": false,
		"reason":    "The work is complete.",
	})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-verbose", "-cake-bin", fake, "-data-dir", temp, "--", testID})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	var doc suggest.Document
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Suggested || doc.Reason != "The work is complete." {
		t.Fatalf("unexpected document: %+v", doc)
	}
	for name, path := range map[string]string{"resume": resumeArgs, "excerpt": excerptArgs} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s ran after a valid reuse no-op: %v", name, err)
		}
	}
	if !strings.Contains(stderr, "mode=reuse") {
		t.Errorf("verbose output does not name the reuse mode: %s", stderr)
	}
}

func TestSuggestReuseAndResumeFailureFallsBackToExcerpt(t *testing.T) {
	project := t.TempDir()
	fake := writeFakeCake(t)
	temp := t.TempDir()
	reuseArgs := filepath.Join(temp, "reuse-args")
	resumeArgs := filepath.Join(temp, "resume-args")
	excerptArgs := filepath.Join(temp, "excerpt-args")
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(project)))
	writeCapture(t, temp, nil)
	t.Setenv("FAKE_REUSE_ARGS_FILE", reuseArgs)
	t.Setenv("FAKE_REUSE_EXIT", "1")
	t.Setenv("FAKE_REUSE_STDERR", "request capture stale")
	t.Setenv("FAKE_RESUME_ARGS_FILE", resumeArgs)
	t.Setenv("FAKE_RESUME_EXIT", "1")
	t.Setenv("FAKE_RESUME_STDERR", "resume unavailable")
	t.Setenv("FAKE_ARGS_FILE", excerptArgs)
	documentEnv(t, map[string]any{
		"suggested":  true,
		"suggestion": "Add a test for the retry path.",
	})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{"suggest", "-json", "-verbose", "-cake-bin", fake, "-data-dir", temp, "--", testID})
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
	for name, path := range map[string]string{"reuse": reuseArgs, "resume": resumeArgs, "excerpt": excerptArgs} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s call did not run: %v", name, err)
		}
	}
	for _, want := range []string{"mode=excerpt", "request reuse failed", "read-only resume failed", "request capture stale", "resume unavailable"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("verbose output does not contain %q: %s", want, stderr)
		}
	}
}

func TestSuggestFallsBackToTheExcerpt(t *testing.T) {
	project := t.TempDir()
	fake := writeFakeCake(t)
	temp := t.TempDir()
	resumeArgs := filepath.Join(temp, "resume-args")
	resumeStdin := filepath.Join(temp, "resume-stdin")
	excerptArgs := filepath.Join(temp, "excerpt-args")
	excerptStdin := filepath.Join(temp, "excerpt-stdin")
	promptPath := filepath.Join(temp, "prompt")
	schemaPath := filepath.Join(temp, "schema")
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(project)))
	t.Setenv("FAKE_RESUME_ARGS_FILE", resumeArgs)
	t.Setenv("FAKE_RESUME_STDIN_FILE", resumeStdin)
	t.Setenv("FAKE_RESUME_EXIT", "1")
	t.Setenv("FAKE_RESUME_STDERR", "error: unexpected argument '--read-only' found")
	t.Setenv("FAKE_ARGS_FILE", excerptArgs)
	t.Setenv("FAKE_STDIN_FILE", excerptStdin)
	t.Setenv("FAKE_PROMPT_FILE", promptPath)
	t.Setenv("FAKE_SCHEMA_FILE", schemaPath)
	documentEnv(t, map[string]any{
		"suggested":  true,
		"suggestion": "Add a test for the retry path.",
	})

	code, stdout, stderr := captureOutput(t, func() int {
		return runCLI([]string{
			"suggest", "-json", "-verbose",
			"-cake-bin", fake,
			"-data-dir", temp,
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
	if !doc.Suggested || doc.Suggestion != "Add a test for the retry path." {
		t.Fatalf("unexpected document: %+v", doc)
	}

	if args := readFile(t, resumeArgs); !strings.Contains(args, "--read-only") {
		t.Errorf("resume was not attempted:\n%s", args)
	}
	if stdin := readFile(t, resumeStdin); !strings.Contains(stdin, "END OF SUGGESTION REQUEST") {
		t.Errorf("resume stdin missing the suggestion request:\n%s", stdin)
	}
	excerpt := readFile(t, excerptArgs)
	for _, want := range []string{
		"--no-session", "--no-tools", "--no-skills",
		"--output-format\njson", "--system-prompt", "--output-schema", "\n-",
	} {
		if !strings.Contains(excerpt, want) {
			t.Errorf("excerpt args do not contain %q:\n%s", want, excerpt)
		}
	}
	if strings.Contains(excerpt, "--resume") {
		t.Errorf("excerpt args must not resume the session:\n%s", excerpt)
	}
	if stdin := readFile(t, excerptStdin); !strings.Contains(stdin, "USER:\nAdd a retry test.") ||
		!strings.Contains(stdin, "END OF TRANSCRIPT") {
		t.Errorf("excerpt stdin is missing the bounded transcript:\n%s", stdin)
	}
	if prompt := readFile(t, promptPath); !strings.Contains(strings.ToLower(prompt), "untrusted") {
		t.Errorf("excerpt system prompt does not call the transcript untrusted")
	}

	if !strings.Contains(stderr, "mode=excerpt") || !strings.Contains(stderr, "fallback:") {
		t.Errorf("verbose output does not name the fallback: %s", stderr)
	}
	if !strings.Contains(stderr, "unexpected argument") {
		t.Errorf("verbose output lost the fallback reason: %s", stderr)
	}
	if strings.Contains(stderr, "Add a retry test.") {
		t.Errorf("verbose output leaked transcript content: %s", stderr)
	}
}

func TestSuggestSkipsResumeWhenItCannotRun(t *testing.T) {
	cases := []struct {
		name         string
		workingDir   func(t *testing.T) string
		modelConfig  string
		modelFlag    string
		wantFallback string
	}{
		{
			name:         "missing working directory",
			modelConfig:  "test-config",
			wantFallback: "no working directory",
		},
		{
			name:         "relative working directory",
			workingDir:   func(*testing.T) string { return "relative/project" },
			modelConfig:  "test-config",
			wantFallback: "not absolute",
		},
		{
			name:         "model mismatch",
			workingDir:   func(t *testing.T) string { return t.TempDir() },
			modelConfig:  "session-model",
			modelFlag:    "other-model",
			wantFallback: "differs",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := writeFakeCake(t)
			temp := t.TempDir()
			resumeArgs := filepath.Join(temp, "resume-args")
			excerptArgs := filepath.Join(temp, "excerpt-args")
			excerptStdin := filepath.Join(temp, "excerpt-stdin")
			workingDir := ""
			if tc.workingDir != nil {
				workingDir = tc.workingDir(t)
			}
			meta := sessionMeta(workingDir, tc.modelConfig)
			t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayWithMeta(meta)))
			t.Setenv("FAKE_RESUME_ARGS_FILE", resumeArgs)
			t.Setenv("FAKE_ARGS_FILE", excerptArgs)
			t.Setenv("FAKE_STDIN_FILE", excerptStdin)
			documentEnv(t, map[string]any{
				"suggested":  true,
				"suggestion": "Add a test for the retry path.",
			})

			args := []string{"suggest", "-json", "-verbose", "-cake-bin", fake, "-data-dir", temp}
			if tc.modelFlag != "" {
				args = append(args, "-model", tc.modelFlag)
			}
			args = append(args, "--", testID)
			code, stdout, stderr := captureOutput(t, func() int { return runCLI(args) })
			if code != 0 {
				t.Fatalf("exit code = %d, stderr: %s", code, stderr)
			}
			var doc suggest.Document
			if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
				t.Fatalf("invalid result JSON: %v\n%s", err, stdout)
			}
			if !doc.Suggested {
				t.Fatalf("unexpected document: %+v", doc)
			}
			if _, err := os.Stat(resumeArgs); !os.IsNotExist(err) {
				t.Fatalf("resume ran when it should have been skipped: %v", err)
			}
			if stdin := readFile(t, excerptStdin); !strings.Contains(stdin, "USER:\nAdd a retry test.") {
				t.Errorf("excerpt stdin is missing the bounded transcript:\n%s", stdin)
			}
			if !strings.Contains(stderr, "mode=excerpt") || !strings.Contains(stderr, tc.wantFallback) {
				t.Errorf("verbose output = %s, want fallback %q", stderr, tc.wantFallback)
			}
			if tc.modelFlag != "" && !strings.Contains(readFile(t, excerptArgs), "-model\n"+tc.modelFlag) {
				t.Errorf("excerpt did not honor the requested model:\n%s", readFile(t, excerptArgs))
			}
		})
	}
}

func TestSuggestNoOp(t *testing.T) {
	project := t.TempDir()
	fake := writeFakeCake(t)
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(project)))
	argsPath := filepath.Join(t.TempDir(), "resume-args")
	excerptPath := filepath.Join(t.TempDir(), "excerpt-args")
	t.Setenv("FAKE_RESUME_ARGS_FILE", argsPath)
	t.Setenv("FAKE_ARGS_FILE", excerptPath)
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
	if _, err := os.Stat(excerptPath); !os.IsNotExist(err) {
		t.Fatalf("a valid no-op fell back to the excerpt: %v", err)
	}
}

func TestSuggestNoConversationSkipsTheModel(t *testing.T) {
	fake := writeFakeCake(t)
	project := t.TempDir()
	replay := strings.Join([]string{
		sessionMeta(project, "test-config"),
		`{"type":"turn_usage","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
	}, "\n") + "\n"
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replay))
	resumeArgs := filepath.Join(t.TempDir(), "resume-args")
	excerptArgs := filepath.Join(t.TempDir(), "excerpt-args")
	t.Setenv("FAKE_RESUME_ARGS_FILE", resumeArgs)
	t.Setenv("FAKE_ARGS_FILE", excerptArgs)

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
	if doc.Suggested || !strings.Contains(doc.Reason, "no user or assistant") {
		t.Fatalf("unexpected document: %+v", doc)
	}
	if !strings.Contains(stderr, "mode=none") {
		t.Errorf("verbose output does not name the no-call mode: %s", stderr)
	}
	for name, path := range map[string]string{"resume": resumeArgs, "excerpt": excerptArgs} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s model call ran for an empty transcript: %v", name, err)
		}
	}
}

func TestSuggestRejectsInvalidModelDocument(t *testing.T) {
	fake := writeFakeCake(t)
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(t.TempDir())))
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
	t.Setenv("FAKE_REPLAY_FILE", writeReplay(t, replayFixture(t.TempDir())))
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
