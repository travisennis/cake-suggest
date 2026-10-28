package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestBinaryDefault(t *testing.T) {
	if Binary != "0.1.0" {
		t.Errorf("Binary = %q, want %q", Binary, "0.1.0")
	}
}

func TestText(t *testing.T) {
	tests := []struct {
		name   string
		binary string
		info   *debug.BuildInfo
		want   string
	}{
		{
			name:   "no build info",
			binary: "0.1.0",
			info:   nil,
			want:   "cake-suggest 0.1.0",
		},
		{
			name:   "no VCS settings",
			binary: "0.1.0",
			info:   &debug.BuildInfo{},
			want:   "cake-suggest 0.1.0",
		},
		{
			name:   "time without revision",
			binary: "0.1.0",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.time", Value: "2026-10-28T14:14:30Z"},
			}},
			want: "cake-suggest 0.1.0",
		},
		{
			name:   "revision and time",
			binary: "v1.0.0",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "7e21261b99bbc1b4c6afcdc080421a431181d415"},
				{Key: "vcs.time", Value: "2026-10-28T14:14:30Z"},
				{Key: "vcs.modified", Value: "false"},
			}},
			want: "cake-suggest v1.0.0\nrevision 7e21261b99bbc1b4c6afcdc080421a431181d415\ntime 2026-10-28T14:14:30Z",
		},
		{
			name:   "modified revision",
			binary: "0.1.0",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abc123"},
				{Key: "vcs.modified", Value: "true"},
			}},
			want: "cake-suggest 0.1.0\nrevision abc123 (modified)",
		},
		{
			name:   "modified revision with time",
			binary: "0.1.0",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abc123"},
				{Key: "vcs.time", Value: "2026-10-28T14:14:30Z"},
				{Key: "vcs.modified", Value: "true"},
			}},
			want: "cake-suggest 0.1.0\nrevision abc123 (modified)\ntime 2026-10-28T14:14:30Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := Binary
			Binary = tt.binary
			t.Cleanup(func() { Binary = orig })

			if got := text(tt.info); got != tt.want {
				t.Fatalf("text() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTextReportsBuildRevision(t *testing.T) {
	orig := Binary
	Binary = "test"
	t.Cleanup(func() { Binary = orig })

	got := Text()
	if !strings.HasPrefix(got, "cake-suggest test") {
		t.Fatalf("Text() = %q, want it to start with %q", got, "cake-suggest test")
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if setting.Value != "" && !strings.Contains(got, setting.Value) {
				t.Fatalf("Text() does not include build revision %q:\n%s", setting.Value, got)
			}
		case "vcs.time":
			if setting.Value != "" && !strings.Contains(got, setting.Value) {
				t.Fatalf("Text() does not include commit time %q:\n%s", setting.Value, got)
			}
		}
	}
}
