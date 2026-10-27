package codex

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseEvents(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"thread.started","thread_id":"0199"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"hello"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":12,"output_tokens":3}}`,
	}, "\n") + "\n"
	var dump bytes.Buffer
	res := ParseEvents(strings.NewReader(input), &dump)
	if res.ThreadID != "0199" {
		t.Fatalf("thread id = %q", res.ThreadID)
	}
	if res.LastMessage != "hello" {
		t.Fatalf("last message = %q", res.LastMessage)
	}
	if res.Usage == nil || res.Usage.InputTokens != 12 || res.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", res.Usage)
	}
	if res.Events != 4 || res.BadLines != 0 {
		t.Fatalf("events=%d bad=%d", res.Events, res.BadLines)
	}
	if dump.String() != input {
		t.Fatalf("raw events not preserved:\n%s", dump.String())
	}
}

func TestParseEventsFailuresAndBadLines(t *testing.T) {
	input := "not json\n" +
		`{"type":"turn.failed","error":{"message":"model overloaded"}}` + "\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"last"}}` + "\n"
	res := ParseEvents(strings.NewReader(input), nil)
	if res.BadLines != 1 {
		t.Fatalf("bad lines = %d", res.BadLines)
	}
	if res.Failure != "model overloaded" {
		t.Fatalf("failure = %q", res.Failure)
	}
	if res.LastMessage != "last" {
		t.Fatalf("last message = %q", res.LastMessage)
	}
}

func TestParseEventsErrorEvent(t *testing.T) {
	res := ParseEvents(strings.NewReader(`{"type":"error","message":"boom"}`+"\n"), nil)
	if res.Failure != "boom" {
		t.Fatalf("failure = %q", res.Failure)
	}
}
