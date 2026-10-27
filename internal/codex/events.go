package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
)

// maxParseLine bounds how much of one JSONL line is decoded. Larger lines are
// still copied to the raw events file, just not inspected for metadata.
const maxParseLine = 8 << 20

const maxLastMessage = 4 << 20

// ParseResult is the metadata extracted from a JSONL event stream.
type ParseResult struct {
	ThreadID    string
	Usage       *Usage
	Failure     string
	LastMessage string
	Events      int
	BadLines    int
}

type event struct {
	Type     string      `json:"type"`
	ThreadID string      `json:"thread_id"`
	Usage    *Usage      `json:"usage"`
	Error    *eventError `json:"error"`
	Message  string      `json:"message"`
	Item     *item       `json:"item"`
}

type eventError struct {
	Message string `json:"message"`
}

type item struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Message string `json:"message"`
}

// ParseEvents copies every raw line to dump (when non-nil) and extracts the
// handful of fields the job manager needs. Malformed lines are counted, not
// fatal: Codex may emit warnings on stdout.
func ParseEvents(r io.Reader, dump io.Writer) ParseResult {
	var res ParseResult
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			res.Events++
			if dump != nil {
				_, _ = dump.Write(line)
			}
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 {
				if len(trimmed) > maxParseLine {
					res.BadLines++
				} else {
					var ev event
					if json.Unmarshal(trimmed, &ev) != nil {
						res.BadLines++
					} else {
						res.apply(ev)
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	return res
}

func (r *ParseResult) apply(ev event) {
	switch ev.Type {
	case "thread.started":
		if ev.ThreadID != "" {
			r.ThreadID = ev.ThreadID
		}
	case "turn.completed":
		if ev.Usage != nil {
			u := *ev.Usage
			r.Usage = &u
		}
	case "turn.failed":
		r.Failure = firstMessage(ev)
	case "error":
		if ev.Message != "" {
			r.Failure = ev.Message
		}
	}
	if ev.Item == nil || ev.Type != "item.completed" {
		return
	}
	switch ev.Item.Type {
	case "agent_message":
		if ev.Item.Text != "" {
			msg := ev.Item.Text
			if len(msg) > maxLastMessage {
				msg = msg[:maxLastMessage]
			}
			r.LastMessage = msg
		}
	case "error":
		if r.Failure == "" && ev.Item.Message != "" {
			r.Failure = ev.Item.Message
		}
	}
}

func firstMessage(ev event) string {
	if ev.Error != nil && ev.Error.Message != "" {
		return ev.Error.Message
	}
	return ev.Message
}
