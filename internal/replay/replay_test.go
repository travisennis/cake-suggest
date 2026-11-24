package replay

import (
	"strings"
	"testing"
)

const testSessionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func meta(extra string) string {
	record := `{"type":"session_meta","format_version":4,"session_id":"` + testSessionID + `"`
	if extra != "" {
		record += "," + extra
	}
	return record + "}"
}

func stream(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func TestParseReadsSessionMetadata(t *testing.T) {
	data := stream(
		meta(`"working_directory":"/tmp/project","model":"test-model","model_config":"test-config"`),
		`{"type":"message","role":"user","content":"Add a retry test."}`,
		`{"type":"message","role":"assistant","content":"I added the test and it passes."}`,
	)
	got, err := Parse([]byte(data), testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != testSessionID {
		t.Fatalf("SessionID = %q", got.SessionID)
	}
	if got.WorkingDirectory != "/tmp/project" {
		t.Fatalf("WorkingDirectory = %q", got.WorkingDirectory)
	}
	if got.LastTaskID != "" {
		t.Fatalf("LastTaskID = %q, want empty", got.LastTaskID)
	}
}

func TestParseTracksTheLatestTask(t *testing.T) {
	data := stream(
		meta(""),
		`{"type":"task_start","session_id":"`+testSessionID+`","task_id":"task-one"}`,
		`{"type":"task_start","session_id":"`+testSessionID+`","task_id":"task-two"}`,
		`{"type":"task_complete","session_id":"`+testSessionID+`","task_id":"task-two"}`,
		`{"type":"message","role":"user","content":"hello"}`,
	)
	got, err := Parse([]byte(data), testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastTaskID != "task-two" {
		t.Fatalf("LastTaskID = %q, want task-two", got.LastTaskID)
	}
}

func TestParseToleratesCompactionRecords(t *testing.T) {
	data := stream(
		meta(""),
		`{"type":"compaction","checkpoint_version":1,"checkpoint_id":"checkpoint-1","session_id":"`+testSessionID+`","summary":"Earlier work.","model":{"model":"test-model","model_config":"test-config"}}`,
		`{"type":"message","role":"user","content":"Now document it."}`,
	)
	got, err := Parse([]byte(data), testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != testSessionID {
		t.Fatalf("SessionID = %q", got.SessionID)
	}
}

func TestParseRejectsAnotherSession(t *testing.T) {
	other := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	_, err := Parse([]byte(stream(meta(""))), other)
	if err == nil || !strings.Contains(err.Error(), "not the requested") {
		t.Fatalf("err = %v, want a session mismatch", err)
	}
}

func TestParseReplayError(t *testing.T) {
	data := `{"type":"replay_error","kind":"session_not_found","error":"no such session","exit_code":3}`
	_, err := Parse([]byte(data), testSessionID)
	if err == nil || !strings.Contains(err.Error(), "session_not_found") {
		t.Fatalf("err = %v, want the replay error", err)
	}
}

func TestParseRequiresSessionMeta(t *testing.T) {
	data := `{"type":"message","role":"user","content":"hello"}`
	_, err := Parse([]byte(data), testSessionID)
	if err == nil || !strings.Contains(err.Error(), "session_meta") {
		t.Fatalf("err = %v, want a missing session_meta error", err)
	}
}

func TestParseRejectsMoreThanOneIdentity(t *testing.T) {
	other := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	data := stream(
		meta(""),
		`{"type":"session_meta","format_version":4,"session_id":"`+other+`"}`,
	)
	_, err := Parse([]byte(data), "")
	if err == nil || !strings.Contains(err.Error(), "more than one session identity") {
		t.Fatalf("err = %v, want a duplicate identity error", err)
	}
}

func TestParseRejectsInvalidJSON(t *testing.T) {
	_, err := Parse([]byte("not json\n"), testSessionID)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("err = %v, want invalid JSON", err)
	}
}
