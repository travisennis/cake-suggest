package suggest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/travisennis/cake-suggest/internal/transcript"
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

func TestOutputSchemaIsValidJSON(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(OutputSchema()), &schema); err != nil {
		t.Fatalf("output schema is invalid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v, want object", schema["type"])
	}
}
