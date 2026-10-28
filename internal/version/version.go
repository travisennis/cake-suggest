// Package version reports the cake-suggest binary version and embedded build
// identity.
package version

import (
	"runtime/debug"
	"strings"
)

// Binary is the cake-suggest binary version, overridden by release ldflags.
var Binary = "0.1.0"

// Text returns the version line followed by the embedded VCS revision and
// commit time when the build carries them. A dirty worktree is marked
// "(modified)" on the revision line.
func Text() string {
	info, _ := debug.ReadBuildInfo()
	return text(info)
}

// text formats build information without reading the process-wide runtime
// metadata, so tests can supply synthetic values.
func text(info *debug.BuildInfo) string {
	lines := []string{"cake-suggest " + Binary}
	if info == nil {
		return lines[0]
	}

	var revision, commitTime string
	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.time":
			commitTime = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		return lines[0]
	}

	revisionLine := "revision " + revision
	if modified {
		revisionLine += " (modified)"
	}
	lines = append(lines, revisionLine)
	if commitTime != "" {
		lines = append(lines, "time "+commitTime)
	}
	return strings.Join(lines, "\n")
}
