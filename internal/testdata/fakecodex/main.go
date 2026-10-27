// Command fakecodex is a deterministic stand-in for `codex exec --json` in tests.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	scenario := os.Getenv("FAKE_CODEX_SCENARIO")
	if scenario == "" {
		scenario = "success"
	}
	promptBytes, _ := io.ReadAll(os.Stdin)
	prompt := strings.TrimSpace(string(promptBytes))
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "No prompt provided via stdin.")
		os.Exit(2)
	}

	finalPath := findArg(os.Args, "-o", "--output-last-message")
	const threadID = "0199f3c2-0000-7000-8000-000000000001"

	writeEvent(map[string]any{"type": "thread.started", "thread_id": threadID})
	writeEvent(map[string]any{"type": "turn.started"})

	final := "echo: " + prompt + "\nsecond line"
	switch scenario {
	case "sleep":
		ms := 60000
		if v := os.Getenv("FAKE_CODEX_SLEEP_MS"); v != "" {
			if n, err := time.ParseDuration(v + "ms"); err == nil {
				ms = int(n / time.Millisecond)
			}
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		final = "slept"
	case "fail":
		fmt.Fprintln(os.Stderr, "fake stderr: something went wrong")
		final = "partial answer"
		writeEvent(map[string]any{"type": "turn.failed", "error": map[string]string{"message": "fake turn failure"}})
		writeFinal(finalPath, final)
		os.Exit(1)
	case "big":
		final = strings.Repeat("0123456789", 10*1024) + "\nTHE-END\n"
	case "bigjson":
		final = `{"blob":"` + strings.Repeat("x", 70*1024) + `"}`
	case "json":
		final = `{"ok":true,"message":"fits"}`
	case "badjson":
		fmt.Println("this is not json")
		final = "badjson-ok"
	case "none":
		writeEvent(map[string]any{"type": "turn.completed", "usage": usage()})
		os.Exit(0)
	}

	writeEvent(map[string]any{
		"type": "item.completed",
		"item": map[string]any{"id": "item_1", "type": "agent_message", "text": "event-visible"},
	})
	writeEvent(map[string]any{"type": "turn.completed", "usage": usage()})
	writeFinal(finalPath, final)
}

func writeEvent(v any) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(v)
}

func writeFinal(path, text string) {
	if path == "" {
		return
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		fmt.Fprintln(os.Stderr, "fakecodex: write final:", err)
		os.Exit(1)
	}
}

func usage() map[string]int64 {
	return map[string]int64{
		"input_tokens":            1234,
		"cached_input_tokens":     1000,
		"output_tokens":           567,
		"reasoning_output_tokens": 12,
	}
}

func findArg(args []string, names ...string) string {
	for i, arg := range args {
		for _, name := range names {
			if arg == name && i+1 < len(args) {
				return args[i+1]
			}
		}
	}
	return ""
}
