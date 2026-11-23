package cakecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveDataDir(t *testing.T) {
	t.Setenv("CAKE_DATA_DIR", "")
	want := filepath.Join(t.TempDir(), "data")
	got, err := ResolveDataDir(want)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ResolveDataDir(%q) = %q", want, got)
	}

	t.Setenv("CAKE_DATA_DIR", want)
	got, err = ResolveDataDir("")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ResolveDataDir from env = %q, want %q", got, want)
	}
}

func TestResolveCacheDir(t *testing.T) {
	t.Setenv("CAKE_DATA_DIR", "")
	dataDir := filepath.Join(t.TempDir(), "data")
	got, err := ResolveCacheDir(dataDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if got != dataDir {
		t.Fatalf("ResolveCacheDir(configured) = %q, want %q", got, dataDir)
	}

	if runtime.GOOS == "windows" {
		t.Skip("the default cache path uses HOME on Unix")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err = ResolveCacheDir(dataDir, false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".cache", "cake")
	if got != want {
		t.Fatalf("ResolveCacheDir(default) = %q, want %q", got, want)
	}
}

func TestValidSessionID(t *testing.T) {
	if !ValidSessionID("01234567-89ab-4cde-8f01-23456789abcd") {
		t.Fatal("valid UUID was rejected")
	}
	for _, id := range []string{"", "..", "../01234567-89ab-4cde-8f01-23456789abcd", "01234567-89AB-4cde-8f01-23456789abcd"} {
		if ValidSessionID(id) {
			t.Errorf("ValidSessionID(%q) = true, want false", id)
		}
	}
}

func TestUsageParsesCachedTokens(t *testing.T) {
	raw := []byte(`{"input_tokens":500,"input_tokens_details":{"cached_tokens":321,"cache_write_tokens":12},"output_tokens":50,"total_tokens":550}`)
	var usage Usage
	if err := json.Unmarshal(raw, &usage); err != nil {
		t.Fatal(err)
	}
	if usage.InputTokensDetails.CachedTokens != 321 || usage.InputTokensDetails.CacheWriteTokens != 12 {
		t.Fatalf("cached usage = %+v", usage.InputTokensDetails)
	}
}

func TestResolveBinaryWithPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cake")
	got, err := ResolveBinary(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("ResolveBinary(%q) = %q", path, got)
	}
}

func TestResolveBinaryFromPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable lookup")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cake-test")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAKE_BIN", "")
	t.Setenv("PATH", dir)
	got, err := ResolveBinary("cake-test")
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("ResolveBinary from PATH = %q, want %q", got, path)
	}
}

func TestCapBufferKeepsTheHeadAndMarksOverflow(t *testing.T) {
	buffer := &capBuffer{limit: 5}
	if _, err := buffer.Write([]byte("abcdefghij")); err != nil {
		t.Fatal(err)
	}
	if !buffer.overflow {
		t.Fatal("overflow = false, want true")
	}
	if got := buffer.String(); got != "abcde" {
		t.Fatalf("buffer = %q, want the head", got)
	}
}

func TestTailBufferKeepsTheTail(t *testing.T) {
	buffer := &tailBuffer{limit: 5}
	if _, err := buffer.Write([]byte("abcdefghij")); err != nil {
		t.Fatal(err)
	}
	if got := buffer.String(); got != "fghij" {
		t.Fatalf("buffer = %q, want the tail", got)
	}
}

func TestEnvironmentWithReplacesDuplicates(t *testing.T) {
	t.Setenv("CAKE_SUGGEST_TEST_VALUE", "old")
	env := environmentWith("CAKE_SUGGEST_TEST_VALUE", "new")
	found := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "CAKE_SUGGEST_TEST_VALUE=") {
			found++
			if entry != "CAKE_SUGGEST_TEST_VALUE=new" {
				t.Fatalf("entry = %q", entry)
			}
		}
	}
	if found != 1 {
		t.Fatalf("found %d entries, want 1", found)
	}
}
