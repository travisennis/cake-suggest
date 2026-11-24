//go:build !windows

package cakecli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReuseRunsTheCapturedRequest(t *testing.T) {
	temp := t.TempDir()
	argsFile := filepath.Join(temp, "args")
	stdinFile := filepath.Join(temp, "stdin")
	schemaFile := filepath.Join(temp, "schema")
	cwdFile := filepath.Join(temp, "cwd")
	dataDirFile := filepath.Join(temp, "data-dir")
	script := filepath.Join(temp, "fake-cake")
	body := `#!/bin/sh
set -eu
want_schema=0
printf '%s\n' "$@" > "$FAKE_ARGS_FILE"
cat > "$FAKE_STDIN_FILE"
pwd > "$FAKE_CWD_FILE"
printf '%s' "$CAKE_DATA_DIR" > "$FAKE_DATA_DIR_FILE"
for arg in "$@"; do
  if [ "$want_schema" = 1 ]; then
    cat "$arg" > "$FAKE_SCHEMA_FILE"
    want_schema=0
  elif [ "$arg" = "--output-schema" ]; then
    want_schema=1
  fi
done
printf '{"result":"{\\"suggested\\":true,\\"suggestion\\":\\"Add a test.\\"}","usage":{"input_tokens":500,"input_tokens_details":{"cached_tokens":321,"cache_write_tokens":12},"output_tokens":50,"total_tokens":550}}\n'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_ARGS_FILE", argsFile)
	t.Setenv("FAKE_STDIN_FILE", stdinFile)
	t.Setenv("FAKE_SCHEMA_FILE", schemaFile)
	t.Setenv("FAKE_CWD_FILE", cwdFile)
	t.Setenv("FAKE_DATA_DIR_FILE", dataDirFile)

	workingDir := t.TempDir()
	result, usage, err := Reuse(context.Background(), ReuseOptions{
		Binary:           script,
		DataDir:          "/tmp/cake-data",
		SessionID:        "01234567-89ab-4cde-8f01-23456789abcd",
		WorkingDirectory: workingDir,
		Model:            "test-config",
		Profile:          "concise",
		OutputSchema:     `{"type":"object"}`,
		Instructions:     "suggestion instructions",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Add a test.") {
		t.Fatalf("result = %q", result)
	}
	if usage.InputTokensDetails.CachedTokens != 321 || usage.InputTokensDetails.CacheWriteTokens != 12 {
		t.Fatalf("usage = %+v", usage)
	}

	args := readTestFile(t, argsFile)
	for _, want := range []string{
		"--resume\n01234567-89ab-4cde-8f01-23456789abcd",
		"--reuse-last-request",
		"--output-format\njson",
		"--output-schema",
		"--model\ntest-config",
		"--profile\nconcise",
		"\n-",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args do not contain %q:\n%s", want, args)
		}
	}
	for _, forbidden := range []string{
		"--read-only", "--tool-choice", "--reasoning-effort",
		"--no-session", "--no-tools", "--no-skills", "--system-prompt",
	} {
		if strings.Contains(args, forbidden) {
			t.Errorf("args must not contain %q:\n%s", forbidden, args)
		}
	}
	if got := readTestFile(t, stdinFile); got != "suggestion instructions" {
		t.Fatalf("stdin = %q", got)
	}
	if got := readTestFile(t, schemaFile); got != `{"type":"object"}` {
		t.Fatalf("schema = %q", got)
	}
	if got := strings.TrimSpace(readTestFile(t, cwdFile)); !sameTestPath(t, got, workingDir) {
		t.Fatalf("cwd = %q, want %q", got, workingDir)
	}
	if got := readTestFile(t, dataDirFile); got != "/tmp/cake-data" {
		t.Fatalf("CAKE_DATA_DIR = %q", got)
	}
}

func TestReuseReportsACakeFailure(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-cake")
	body := "#!/bin/sh\nprintf 'capture stale' >&2\nexit 3\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := Reuse(context.Background(), ReuseOptions{
		Binary:       script,
		SessionID:    "01234567-89ab-4cde-8f01-23456789abcd",
		OutputSchema: `{"type":"object"}`,
		Instructions: "instructions",
	})
	if err == nil || !strings.Contains(err.Error(), "request reuse failed: capture stale") {
		t.Fatalf("err = %v", err)
	}
}

func TestReuseRejectsAnEmptyResult(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-cake")
	body := "#!/bin/sh\nprintf '{\"result\":\"\"}\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := Reuse(context.Background(), ReuseOptions{
		Binary:       script,
		SessionID:    "01234567-89ab-4cde-8f01-23456789abcd",
		OutputSchema: `{"type":"object"}`,
		Instructions: "instructions",
	})
	if err == nil || !strings.Contains(err.Error(), "empty result") {
		t.Fatalf("err = %v", err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func sameTestPath(t *testing.T, got, want string) bool {
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
