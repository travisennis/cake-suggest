//go:build !windows

package cakecli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSuggestIsolatesTheChildConfig(t *testing.T) {
	sourceRoot := t.TempDir()
	sourceCake := filepath.Join(sourceRoot, "cake")
	if err := os.MkdirAll(sourceCake, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := "default_model = \"test-model\"\n"
	if err := os.WriteFile(filepath.Join(sourceCake, "settings.toml"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceCake, "hooks.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", sourceRoot)

	record := filepath.Join(t.TempDir(), "child-env")
	script := filepath.Join(t.TempDir(), "fake-cake")
	body := `#!/bin/sh
set -eu
{
  printf 'XDG_CONFIG_HOME=%s\n' "$XDG_CONFIG_HOME"
  printf 'CAKE_DATA_DIR=%s\n' "$CAKE_DATA_DIR"
  printf 'SETTINGS_BEGIN\n'
  cat "$XDG_CONFIG_HOME/cake/settings.toml"
  printf 'SETTINGS_END\n'
  if [ -e "$XDG_CONFIG_HOME/cake/hooks.json" ]; then
    printf 'HOOKS=yes\n'
  else
    printf 'HOOKS=no\n'
  fi
} > "$FAKE_ENV_FILE"
printf '{"result":"ok"}\n'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_ENV_FILE", record)

	if _, _, err := Suggest(context.Background(), ModelOptions{
		Binary:       script,
		SystemPrompt: "system",
		OutputSchema: `{"type":"object"}`,
		Transcript:   "USER:\nhello\n",
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "XDG_CONFIG_HOME="+sourceRoot) {
		t.Fatalf("child inherited the developer config root:\n%s", got)
	}
	if !strings.Contains(got, "SETTINGS_BEGIN\n"+settings+"SETTINGS_END") {
		t.Fatalf("child did not receive the copied settings:\n%s", got)
	}
	if !strings.Contains(got, "HOOKS=no") {
		t.Fatalf("child received hooks in the isolated config:\n%s", got)
	}
	if !strings.Contains(got, "CAKE_DATA_DIR=") {
		t.Fatalf("child did not receive a temporary data root:\n%s", got)
	}
}
