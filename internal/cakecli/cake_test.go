package cakecli

import (
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
