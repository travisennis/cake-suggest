package transcript

import (
	"strings"
	"testing"
)

const testSessionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func fixture() string {
	return strings.Join([]string{
		`{"type":"session_meta","format_version":4,"session_id":"` + testSessionID + `","working_directory":"/tmp/project","model":"test-model"}`,
		`{"type":"message","role":"user","content":"Add a retry test."}`,
		`{"type":"message","role":"assistant","content":"I added the test and it passes."}`,
		`{"type":"function_call","name":"Bash","call_id":"call-1","arguments":"{\"command\":\"go test ./...\"}"}`,
		`{"type":"function_call_output","call_id":"call-1","output":"ok example.test"}`,
		`{"type":"reasoning","content":[{"type":"reasoning_text","text":"private reasoning"}],"summary":[{"type":"summary_text","text":"private reasoning"}]}`,
		`{"type":"turn_usage","usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}}`,
		`{"type":"compaction","summary":"Earlier work added the retry configuration."}`,
		`{"type":"message","role":"user","content":"Now document it."}`,
	}, "\n") + "\n"
}

func TestParseBuildsTheConversation(t *testing.T) {
	got, err := Parse([]byte(fixture()), testSessionID, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != testSessionID {
		t.Fatalf("SessionID = %q", got.SessionID)
	}
	if got.WorkingDirectory != "/tmp/project" {
		t.Fatalf("WorkingDirectory = %q", got.WorkingDirectory)
	}
	if got.Entries != 6 {
		t.Fatalf("Entries = %d, want 6", got.Entries)
	}
	if got.Omitted != 0 {
		t.Fatalf("Omitted = %d, want 0", got.Omitted)
	}
	for _, want := range []string{
		"SESSION: " + testSessionID,
		"WORKING DIRECTORY: /tmp/project",
		"USER:\nAdd a retry test.",
		"ASSISTANT:\nI added the test and it passes.",
		"TOOL CALL Bash:",
		"go test ./...",
		"TOOL RESULT:\nok example.test",
		"HISTORICAL SUMMARY:\nEarlier work added the retry configuration.",
		"USER:\nNow document it.",
	} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("transcript does not contain %q:\n%s", want, got.Text)
		}
	}
	if strings.Contains(got.Text, "private reasoning") {
		t.Error("transcript contains reasoning content")
	}
}

func TestParseRejectsAnotherSession(t *testing.T) {
	other := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	_, err := Parse([]byte(fixture()), other, 100_000)
	if err == nil || !strings.Contains(err.Error(), "not the requested") {
		t.Fatalf("err = %v, want a session mismatch", err)
	}
}

func TestParseReplayError(t *testing.T) {
	stream := `{"type":"replay_error","kind":"session_not_found","error":"no such session","exit_code":3}`
	_, err := Parse([]byte(stream), testSessionID, 100_000)
	if err == nil || !strings.Contains(err.Error(), "session_not_found") {
		t.Fatalf("err = %v, want the replay error", err)
	}
}

func TestParseRequiresSessionMeta(t *testing.T) {
	stream := `{"type":"message","role":"user","content":"hello"}`
	_, err := Parse([]byte(stream), testSessionID, 100_000)
	if err == nil || !strings.Contains(err.Error(), "session_meta") {
		t.Fatalf("err = %v, want a missing session_meta error", err)
	}
}

func TestParseKeepsNewestEntriesAtBudget(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"session_meta","format_version":4,"session_id":"` + testSessionID + `"}`,
		`{"type":"message","role":"user","content":"OLDEST request"}`,
		`{"type":"message","role":"assistant","content":"MIDDLE answer"}`,
		`{"type":"message","role":"user","content":"NEWEST request"}`,
	}, "\n") + "\n"

	got, err := Parse([]byte(stream), testSessionID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "NEWEST request") {
		t.Fatalf("newest entry was dropped:\n%s", got.Text)
	}
	if strings.Contains(got.Text, "OLDEST request") {
		t.Fatalf("oldest entry survived the budget:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "earlier transcript omitted") {
		t.Fatalf("missing omission marker:\n%s", got.Text)
	}
	if got.Omitted == 0 {
		t.Fatal("Omitted = 0, want at least one")
	}
}

func TestParseClipsOneLongEntry(t *testing.T) {
	long := strings.Repeat("x", 10_000)
	stream := strings.Join([]string{
		`{"type":"session_meta","format_version":4,"session_id":"` + testSessionID + `"}`,
		`{"type":"message","role":"assistant","content":"` + long + `"}`,
	}, "\n") + "\n"

	got, err := Parse([]byte(stream), testSessionID, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "bytes omitted") {
		t.Fatalf("long entry was not clipped:\n%s", got.Text)
	}
	if len(got.Text) > 7000 {
		t.Fatalf("clipped transcript is still %d bytes", len(got.Text))
	}
}

func TestParseArrayContent(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"session_meta","format_version":4,"session_id":"` + testSessionID + `"}`,
		`{"type":"message","role":"assistant","content":[{"type":"text","text":"The test passes."}]}`,
	}, "\n") + "\n"

	got, err := Parse([]byte(stream), testSessionID, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "ASSISTANT:\nThe test passes.") {
		t.Fatalf("array message content was not projected:\n%s", got.Text)
	}
}

func TestParseNoConversation(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"session_meta","format_version":4,"session_id":"` + testSessionID + `"}`,
		`{"type":"turn_usage","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
	}, "\n") + "\n"

	got, err := Parse([]byte(stream), testSessionID, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "" || got.Entries != 0 {
		t.Fatalf("got text=%q entries=%d, want both empty", got.Text, got.Entries)
	}
}

func TestParseRejectsInvalidJSON(t *testing.T) {
	_, err := Parse([]byte("not json\n"), testSessionID, 100_000)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("err = %v, want invalid JSON", err)
	}
}
