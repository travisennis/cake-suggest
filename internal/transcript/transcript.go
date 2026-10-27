// Package transcript turns Cake's read-only replay stream into the bounded
// conversation excerpt a suggester model reads.
//
// It consumes only the documented stream-json record vocabulary. Audit and
// lifecycle records are ignored; message, tool-call, tool-result, and
// checkpoint-summary records become labeled blocks. The newest blocks are kept
// within a character budget so a long session produces a small, current input.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	defaultMaxChars = 20_000
	messageChars    = 6_000
	callChars       = 2_000
	outputChars     = 3_000
	summaryChars    = 8_000
)

// Transcript is a bounded, readable view of one session.
type Transcript struct {
	SessionID        string
	WorkingDirectory string
	Text             string
	Entries          int
	Omitted          int
}

type record struct {
	Type             string          `json:"type"`
	SessionID        string          `json:"session_id"`
	WorkingDirectory string          `json:"working_directory"`
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	Name             string          `json:"name"`
	Arguments        string          `json:"arguments"`
	Output           string          `json:"output"`
	Summary          json.RawMessage `json:"summary"`
	Kind             string          `json:"kind"`
	Error            string          `json:"error"`
	ExitCode         int             `json:"exit_code"`
}

type entry struct {
	block string
}

// Parse decodes a replay stream and returns the newest entries that fit
// maxChars. wantSessionID is the requested identity; a stream for another
// session is a contract failure.
func Parse(data []byte, wantSessionID string, maxChars int) (Transcript, error) {
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}
	result := Transcript{SessionID: wantSessionID}
	entries := make([]entry, 0, 32)
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
				return Transcript{}, fmt.Errorf("replay line %d: invalid JSON: %w", lineNo, err)
			}
			if rec.Type == "session_meta" {
				if rec.SessionID == "" {
					return Transcript{}, fmt.Errorf("replay line %d: session_meta has no session_id", lineNo)
				}
				if wantSessionID != "" && rec.SessionID != wantSessionID {
					return Transcript{}, fmt.Errorf(
						"replay returned session %s, not the requested %s",
						rec.SessionID, wantSessionID,
					)
				}
				if sawMeta && rec.SessionID != result.SessionID {
					return Transcript{}, fmt.Errorf("replay contains more than one session identity")
				}
				sawMeta = true
				result.SessionID = rec.SessionID
				result.WorkingDirectory = rec.WorkingDirectory
			} else {
				block, ok, err := blockFor(rec)
				if err != nil {
					return Transcript{}, fmt.Errorf("replay line %d: %w", lineNo, err)
				}
				if ok {
					entries = append(entries, entry{block: block})
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return Transcript{}, fmt.Errorf("read replay stream: %w", readErr)
		}
	}

	if !sawMeta {
		return Transcript{}, fmt.Errorf("replay stream has no session_meta record")
	}
	result.Entries = len(entries)
	result.Text, result.Omitted = buildText(result, entries, maxChars)
	return result, nil
}

func blockFor(rec record) (string, bool, error) {
	switch rec.Type {
	case "replay_error":
		detail := strings.TrimSpace(rec.Error)
		if detail == "" {
			detail = "no diagnostic"
		}
		if rec.Kind == "" {
			return "", false, fmt.Errorf("cake replay failed: %s", detail)
		}
		return "", false, fmt.Errorf("cake replay failed (%s): %s", rec.Kind, detail)
	case "message":
		switch rec.Role {
		case "user":
			content := strings.TrimSpace(textContent(rec.Content))
			if content == "" {
				return "", false, nil
			}
			return "USER:\n" + clip(content, messageChars), true, nil
		case "assistant":
			content := strings.TrimSpace(textContent(rec.Content))
			if content == "" {
				return "", false, nil
			}
			return "ASSISTANT:\n" + clip(content, messageChars), true, nil
		default:
			// System/tool messages are not part of the user-visible exchange.
			return "", false, nil
		}
	case "function_call":
		name := strings.TrimSpace(rec.Name)
		arguments := strings.TrimSpace(rec.Arguments)
		if name == "" && arguments == "" {
			return "", false, nil
		}
		if name == "" {
			name = "unknown"
		}
		return "TOOL CALL " + name + ":\n" + clip(arguments, callChars), true, nil
	case "function_call_output":
		output := strings.TrimSpace(rec.Output)
		if output == "" {
			return "", false, nil
		}
		return "TOOL RESULT:\n" + clip(output, outputChars), true, nil
	case "compaction":
		var summary string
		if len(rec.Summary) > 0 {
			_ = json.Unmarshal(rec.Summary, &summary)
		}
		summary = strings.TrimSpace(summary)
		if summary == "" {
			return "", false, nil
		}
		return "HISTORICAL SUMMARY:\n" + clip(summary, summaryChars), true, nil
	default:
		return "", false, nil
	}
}

func textContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Text != "" {
				texts = append(texts, part.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

func buildText(info Transcript, entries []entry, maxChars int) (string, int) {
	if len(entries) == 0 {
		return "", 0
	}

	selected := make([]string, 0, len(entries))
	budget := maxChars
	cut := 0
	for i := len(entries) - 1; i >= 0; i-- {
		block := entries[i].block
		if len(block) > budget {
			if len(selected) == 0 {
				selected = append(selected, clip(block, budget))
				budget = 0
				cut = i
			} else {
				cut = i + 1
			}
			break
		}
		selected = append(selected, block)
		budget -= len(block) + len("\n---\n\n")
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}

	var builder strings.Builder
	builder.WriteString("SESSION: ")
	builder.WriteString(info.SessionID)
	if info.WorkingDirectory != "" {
		builder.WriteString("\nWORKING DIRECTORY: ")
		builder.WriteString(info.WorkingDirectory)
	}
	if cut > 0 {
		builder.WriteString("\n\n[... earlier transcript omitted ...]")
	}
	if len(selected) > 0 {
		builder.WriteString("\n\n")
		builder.WriteString(strings.Join(selected, "\n---\n\n"))
	}
	return builder.String(), cut
}

func clip(text string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(text) <= max {
		return text
	}

	headEnd := max * 2 / 3
	for headEnd > 0 && !utf8.RuneStart(text[headEnd]) {
		headEnd--
	}
	tailStart := len(text) - (max - headEnd)
	for tailStart < len(text) && !utf8.RuneStart(text[tailStart]) {
		tailStart++
	}
	if headEnd <= 0 || tailStart >= len(text) || tailStart <= headEnd {
		return truncateToBytes(text, max)
	}
	omitted := tailStart - headEnd
	return text[:headEnd] +
		fmt.Sprintf("\n[... %d bytes omitted ...]\n", omitted) +
		text[tailStart:]
}

func truncateToBytes(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	var builder strings.Builder
	for _, r := range text {
		if builder.Len()+utf8.RuneLen(r) > maxBytes {
			break
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
