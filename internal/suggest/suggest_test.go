package suggest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/travisennis/cake-suggest/internal/cakecli"
	"github.com/travisennis/cake-suggest/internal/transcript"
)

const (
	testSessionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testTaskID    = "11111111-1111-4111-8111-111111111111"
)

func TestNormalizeSuggestion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{name: "plain", input: "Add a test for the retry path.", want: "Add a test for the retry path.", ok: true},
		{name: "whitespace", input: "  Add\n a\ttest  ", want: "Add a test", ok: true},
		{name: "quotes", input: `"Add a test."`, want: "Add a test.", ok: true},
		{name: "control characters", input: "Add\x1b[31m a\x07 test.", want: "Add[31m a test.", ok: true},
		{name: "backticks", input: "`Add a test.`", want: "Add a test.", ok: true},
		{name: "label", input: "Suggestion: Add a test.", want: "Add a test.", ok: true},
		{name: "next prompt label", input: "Next prompt: Add a test.", want: "Add a test.", ok: true},
		{name: "empty", input: "   \n ", want: "", ok: false},
		{name: "only quotes", input: `""`, want: "", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := NormalizeSuggestion(test.input)
			if got != test.want || ok != test.ok {
				t.Fatalf("NormalizeSuggestion(%q) = (%q, %v), want (%q, %v)", test.input, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestNormalizeSuggestionBoundsLength(t *testing.T) {
	got, ok := NormalizeSuggestion(strings.Repeat("word ", 300))
	if !ok {
		t.Fatal("expected a suggestion")
	}
	if len([]rune(got)) > maxSuggestionChars {
		t.Fatalf("suggestion has %d runes, want at most %d", len([]rune(got)), maxSuggestionChars)
	}
}

func TestParseModelDocument(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "suggested", raw: `{"suggested":true,"suggestion":"Add a test."}`},
		{name: "no-op", raw: `{"suggested":false,"reason":"nothing to do"}`},
		{name: "missing field", raw: `{"suggestion":"Add a test."}`, wantErr: true},
		{name: "invalid json", raw: `not json`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseModelDocument(test.raw)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseModelDocument(%q) err = %v, wantErr = %v", test.raw, err, test.wantErr)
			}
		})
	}
}

func TestNoOpReasonDefaults(t *testing.T) {
	if got := noOpReason("  "); got != "no useful next step" {
		t.Fatalf("noOpReason(empty) = %q", got)
	}
	long := strings.Repeat("reason ", 100)
	if got := noOpReason(long); len([]rune(got)) > maxReasonChars {
		t.Fatalf("reason has %d runes, want at most %d", len([]rune(got)), maxReasonChars)
	}
}

func TestBuildSystemPromptCoversTheContract(t *testing.T) {
	prompt := BuildSystemPrompt()
	for _, want := range []string{"untrusted", "one line", "slash command", "suggested", "transcript"} {
		if !strings.Contains(strings.ToLower(prompt), want) {
			t.Errorf("system prompt does not mention %q", want)
		}
	}
}

func TestBuildResumeInstructionsCoversTheContract(t *testing.T) {
	instructions := BuildResumeInstructions()
	for _, want := range []string{
		"untrusted",
		"do not call any tool",
		"one line",
		"slash command",
		"suggested",
		"END OF SUGGESTION REQUEST",
	} {
		if !strings.Contains(strings.ToLower(instructions), strings.ToLower(want)) {
			t.Errorf("resume instructions do not mention %q", want)
		}
	}
	if strings.Contains(instructions, "END OF TRANSCRIPT") {
		t.Error("resume instructions contain the excerpt end marker")
	}
}

func TestBuildReuseInstructionsIncludesTheSchema(t *testing.T) {
	instructions := BuildReuseInstructions()
	for _, want := range []string{
		"$schema",
		"suggested",
		"suggestion",
		"reason",
		"END OF SUGGESTION REQUEST",
	} {
		if !strings.Contains(instructions, want) {
			t.Errorf("reuse instructions do not contain %q", want)
		}
	}
	if strings.Contains(instructions, "END OF TRANSCRIPT") {
		t.Error("reuse instructions contain the excerpt end marker")
	}
}

func TestResumeReadiness(t *testing.T) {
	dir := t.TempDir()
	view := transcript.Transcript{WorkingDirectory: dir, ModelConfig: "session-model"}
	if ok, reason := resumeReadiness(Options{}, view); !ok {
		t.Fatalf("resumeReadiness with a valid directory = false: %s", reason)
	}
	if ok, reason := resumeReadiness(Options{Model: "session-model"}, view); !ok {
		t.Fatalf("resumeReadiness with the session model = false: %s", reason)
	}
	if ok, reason := resumeReadiness(Options{Model: "other-model"}, view); ok ||
		!strings.Contains(reason, "differs") {
		t.Fatalf("resumeReadiness with another model = (%v, %q), want a mismatch", ok, reason)
	}
	if ok, reason := resumeReadiness(Options{}, transcript.Transcript{}); ok ||
		!strings.Contains(reason, "no working directory") {
		t.Fatalf("resumeReadiness without a directory = (%v, %q)", ok, reason)
	}
	if ok, reason := resumeReadiness(Options{}, transcript.Transcript{WorkingDirectory: "relative"}); ok ||
		!strings.Contains(reason, "not absolute") {
		t.Fatalf("resumeReadiness with a relative directory = (%v, %q)", ok, reason)
	}
}

func TestReuseReadiness(t *testing.T) {
	baseView := func() transcript.Transcript {
		return transcript.Transcript{
			SessionID:        testSessionID,
			WorkingDirectory: t.TempDir(),
			ModelConfig:      "test-config",
			LastTaskID:       testTaskID,
		}
	}
	tests := []struct {
		name           string
		noCapture      bool
		mutateCapture  func(map[string]any)
		mutateOptions  func(*Options)
		mutateView     func(*transcript.Transcript)
		wantKind       captureFailureKind
		wantErr        string
		wantWorkingDir bool
	}{
		{name: "valid", wantWorkingDir: true},
		{name: "missing", noCapture: true, wantKind: captureUnavailable},
		{
			name:          "stale task",
			mutateCapture: func(capture map[string]any) { capture["task_id"] = "22222222-2222-4222-8222-222222222222" },
			wantKind:      captureStale,
		},
		{
			name:          "unsupported version",
			mutateCapture: func(capture map[string]any) { capture["version"] = 2 },
			wantKind:      captureIncompatible,
		},
		{
			name:          "body digest",
			mutateCapture: func(capture map[string]any) { capture["body_sha256"] = "bad" },
			wantKind:      captureIncompatible,
		},
		{
			name:          "profile mismatch",
			mutateCapture: func(capture map[string]any) { capture["profile"] = "concise" },
			wantKind:      captureIncompatible,
		},
		{
			name:           "profile match",
			mutateCapture:  func(capture map[string]any) { capture["profile"] = "concise" },
			mutateOptions:  func(options *Options) { options.Profile = "concise" },
			wantWorkingDir: true,
		},
		{
			name:          "requested model mismatch",
			mutateOptions: func(options *Options) { options.Model = "other-model" },
			wantKind:      captureIncompatible,
		},
		{
			name:       "session model mismatch",
			mutateView: func(view *transcript.Transcript) { view.ModelConfig = "other-config" },
			wantKind:   captureIncompatible,
		},
		{
			name:          "reasoning effort",
			mutateOptions: func(options *Options) { options.ReasoningEffort = "high" },
			wantErr:       "does not support -reasoning-effort",
		},
		{
			name:       "missing working directory",
			mutateView: func(view *transcript.Transcript) { view.WorkingDirectory = "" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cacheDir := t.TempDir()
			if !test.noCapture {
				writeTestCapture(t, cacheDir, test.mutateCapture)
			}
			view := baseView()
			if test.mutateView != nil {
				test.mutateView(&view)
			}
			options := Options{SessionID: testSessionID}
			if test.mutateOptions != nil {
				test.mutateOptions(&options)
			}

			plan, err := reuseReadiness(options, view, cacheDir)
			if test.wantKind != "" {
				var failure *captureFailure
				if !errors.As(err, &failure) || failure.kind != test.wantKind {
					t.Fatalf("reuseReadiness error = %v, want kind %s", err, test.wantKind)
				}
				return
			}
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("reuseReadiness error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.wantWorkingDir && plan.WorkingDirectory != view.WorkingDirectory {
				t.Fatalf("working directory = %q, want %q", plan.WorkingDirectory, view.WorkingDirectory)
			}
		})
	}
}

func TestLogResultReportsCachedTokens(t *testing.T) {
	var log bytes.Buffer
	logResult(Options{SessionID: testSessionID, DebugLog: &log}, Result{
		Mode: ModeReuse,
		Usage: cakecli.Usage{
			InputTokens:        500,
			OutputTokens:       50,
			InputTokensDetails: cakecli.InputTokensDetails{CachedTokens: 321, CacheWriteTokens: 12},
		},
	})
	line := log.String()
	for _, want := range []string{
		"mode=reuse",
		"tokens_in=500",
		"tokens_out=50",
		"cached_tokens=321",
		"cache_write_tokens=12",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("debug line does not contain %q: %s", want, line)
		}
	}
}

func writeTestCapture(t *testing.T, cacheDir string, mutate func(map[string]any)) {
	t.Helper()
	body := `{"messages":[{"role":"system","content":"captured"}]}`
	hash := sha256.Sum256([]byte(body))
	capture := map[string]any{
		"version":    captureVersion,
		"session_id": testSessionID,
		"task_id":    testTaskID,
		"profile":    nil,
		"provider_identity": map[string]any{
			"model":        "test-model",
			"model_config": "test-config",
		},
		"backend":     "chat_completions",
		"body":        body,
		"body_sha256": fmt.Sprintf("%x", hash),
		"body_len":    len(body),
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
	if err := os.WriteFile(filepath.Join(dir, testSessionID+".json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOutputSchemaIsValidJSON(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(OutputSchema()), &schema); err != nil {
		t.Fatalf("output schema is invalid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v, want object", schema["type"])
	}
}
