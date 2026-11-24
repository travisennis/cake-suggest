// Package replay reads session metadata from Cake's documented read-only
// replay stream.
//
// The stream is the only session source this tool is allowed to read. It
// supplies the identity and working directory needed to run a suggestion call,
// and the latest task id needed to tell whether a request capture still
// belongs to the session's newest task. The conversation itself is never
// projected here: exact request reuse sends the captured body instead.
package replay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Session is the metadata cake-suggest needs from one replay.
type Session struct {
	// SessionID is the identity the replay reports.
	SessionID string
	// WorkingDirectory is where the session ran. It may be empty or stale.
	WorkingDirectory string
	// LastTaskID is the task id on the newest task_start or task_complete
	// record. A request capture from an older task cannot be reused.
	LastTaskID string
}

type record struct {
	Type             string `json:"type"`
	SessionID        string `json:"session_id"`
	WorkingDirectory string `json:"working_directory"`
	TaskID           string `json:"task_id"`
	Kind             string `json:"kind"`
	Error            string `json:"error"`
}

// Parse decodes a replay stream. A stream for another session, a missing
// session_meta record, or an invalid line is a contract failure.
func Parse(data []byte, wantSessionID string) (Session, error) {
	result := Session{SessionID: wantSessionID}
	sawMeta := false

	reader := bufio.NewReaderSize(bytes.NewReader(data), 1<<20)
	lineNo := 0
	for {
		line, readErr := reader.ReadString('\n')
		lineNo++
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			var rec record
			if err := json.Unmarshal([]byte(trimmed), &rec); err != nil {
				return Session{}, fmt.Errorf("replay line %d: invalid JSON: %w", lineNo, err)
			}
			switch rec.Type {
			case "replay_error":
				detail := strings.TrimSpace(rec.Error)
				if detail == "" {
					detail = "no diagnostic"
				}
				if rec.Kind == "" {
					return Session{}, fmt.Errorf("cake replay failed: %s", detail)
				}
				return Session{}, fmt.Errorf("cake replay failed (%s): %s", rec.Kind, detail)
			case "session_meta":
				if rec.SessionID == "" {
					return Session{}, fmt.Errorf("replay line %d: session_meta has no session_id", lineNo)
				}
				if wantSessionID != "" && rec.SessionID != wantSessionID {
					return Session{}, fmt.Errorf(
						"replay returned session %s, not the requested %s",
						rec.SessionID, wantSessionID,
					)
				}
				if sawMeta && rec.SessionID != result.SessionID {
					return Session{}, fmt.Errorf("replay contains more than one session identity")
				}
				sawMeta = true
				result.SessionID = rec.SessionID
				result.WorkingDirectory = rec.WorkingDirectory
			case "task_start", "task_complete":
				if rec.TaskID != "" {
					result.LastTaskID = rec.TaskID
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return Session{}, fmt.Errorf("read replay stream: %w", readErr)
		}
	}

	if !sawMeta {
		return Session{}, fmt.Errorf("replay stream has no session_meta record")
	}
	return result, nil
}
