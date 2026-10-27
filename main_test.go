package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/buerka/codex-mcp/internal/codex"
	"github.com/buerka/codex-mcp/internal/config"
	"github.com/buerka/codex-mcp/internal/jobs"
	"github.com/buerka/codex-mcp/internal/policy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var fakeCodex string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "codex-mcp-fake")
	if err != nil {
		fmt.Fprintln(os.Stderr, "temp dir:", err)
		os.Exit(1)
	}
	name := "fakecodex"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	fakeCodex = filepath.Join(dir, name)
	build := exec.Command("go", "build", "-o", fakeCodex, "./internal/testdata/fakecodex")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build fake codex: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func testServer(t *testing.T, allowFullAccess bool) *mcp.ClientSession {
	t.Helper()
	cfg := config.Default()
	cfg.CodexBin = fakeCodex
	cfg.JobsDir = t.TempDir()
	cfg.Timeout = 30 * time.Second
	cfg.MaxConcurrency = 1
	cfg.QueueLimit = 4
	cfg.Retention = 20
	cfg.AllowFullAccess = allowFullAccess

	pol, err := policy.New("", allowFullAccess)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := jobs.New(cfg, &codex.ExecRunner{Bin: fakeCodex})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		mgr.Shutdown(ctx)
	})

	server := newServer(newApp(cfg, mgr, pol))
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	go server.Run(ctx, st)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
}

func contentText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func mustText(t *testing.T, res *mcp.CallToolResult, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if res.IsError {
		t.Fatalf("tool error: %s", contentText(res))
	}
	return contentText(res)
}

func mustError(t *testing.T, res *mcp.CallToolResult, err error) string {
	t.Helper()
	if err != nil {
		return err.Error()
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected a tool error, got %q", contentText(res))
	}
	return contentText(res)
}

func callText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := callTool(t, cs, name, args)
	return mustText(t, res, err)
}

func jobID(t *testing.T, text string) string {
	t.Helper()
	for _, field := range strings.Fields(text) {
		if strings.HasPrefix(field, "job=") {
			return strings.TrimPrefix(field, "job=")
		}
	}
	t.Fatalf("no job id in %q", text)
	return ""
}

func TestListTools(t *testing.T) {
	cs := testServer(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	want := []string{"codex_cancel", "codex_list", "codex_output", "codex_result", "codex_run", "codex_status"}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("want %v, got %v", want, names)
	}
}

func TestRunAndResult(t *testing.T) {
	t.Setenv("FAKE_CODEX_SCENARIO", "success")
	cs := testServer(t, false)
	dir := t.TempDir()

	run := callText(t, cs, "codex_run", map[string]any{
		"prompt":      "hello world",
		"cwd":         dir,
		"sandbox":     "read-only",
		"timeout_sec": 30,
	})
	if !strings.Contains(run, "queued") || !strings.Contains(run, "sandbox=read-only") {
		t.Fatalf("unexpected run response: %q", run)
	}
	id := jobID(t, run)

	result := callText(t, cs, "codex_result", map[string]any{
		"job": id, "wait_sec": 10,
	})
	for _, want := range []string{"exit=0", "---", "echo: hello world", "second line", "usage="} {
		if !strings.Contains(result, want) {
			t.Fatalf("result missing %q:\n%s", want, result)
		}
	}

	status := callText(t, cs, "codex_status", map[string]any{"job": id})
	if !strings.Contains(status, "succeeded") || !strings.Contains(status, "exit=0") {
		t.Fatalf("unexpected status: %q", status)
	}

	list := callText(t, cs, "codex_list", map[string]any{})
	if !strings.Contains(list, id) || !strings.Contains(list, "succeeded") {
		t.Fatalf("list missing job: %q", list)
	}
	if filtered := callText(t, cs, "codex_list", map[string]any{"state": "failed"}); filtered != "(no jobs)" {
		t.Fatalf("failed filter = %q", filtered)
	}
}

func TestBigFinalSequentialPaging(t *testing.T) {
	t.Setenv("FAKE_CODEX_SCENARIO", "big")
	cs := testServer(t, false)
	run := callText(t, cs, "codex_run", map[string]any{
		"prompt": "big", "cwd": t.TempDir(), "sandbox": "read-only",
	})
	id := jobID(t, run)

	first := callText(t, cs, "codex_result", map[string]any{"job": id, "wait_sec": 10})
	if !strings.Contains(first, "continue with codex_result job="+id+" offset=32768") {
		t.Fatalf("missing continuation note:\n%s", tail(first, 500))
	}
	if strings.Contains(first, "bytes omitted") {
		t.Fatalf("text final must not elide the middle:\n%s", tail(first, 500))
	}

	last := callText(t, cs, "codex_result", map[string]any{
		"job": id, "offset": 98304,
	})
	if !strings.Contains(last, "THE-END") {
		t.Fatalf("last page missing end marker: %q", tail(last, 200))
	}
	if !strings.Contains(last, "offset=98304") {
		t.Fatalf("last page missing offset: %q", firstLine(last))
	}
}

func TestJSONFinalFits(t *testing.T) {
	t.Setenv("FAKE_CODEX_SCENARIO", "json")
	cs := testServer(t, false)
	run := callText(t, cs, "codex_run", map[string]any{
		"prompt": "json", "cwd": t.TempDir(), "sandbox": "read-only", "output_schema": `{"type":"object"}`,
	})
	id := jobID(t, run)
	result := callText(t, cs, "codex_result", map[string]any{"job": id, "wait_sec": 10})
	if !strings.Contains(result, `{"ok":true,"message":"fits"}`) {
		t.Fatalf("small JSON not returned whole: %q", result)
	}
	if strings.Contains(result, "codex_output") {
		t.Fatalf("small JSON should not need paging: %q", result)
	}
}

func TestJSONFinalNeverPartial(t *testing.T) {
	t.Setenv("FAKE_CODEX_SCENARIO", "bigjson")
	cs := testServer(t, false)
	schema := `{"type":"object","properties":{"blob":{"type":"string"}},"required":["blob"]}`
	run := callText(t, cs, "codex_run", map[string]any{
		"prompt": "json", "cwd": t.TempDir(), "sandbox": "read-only", "output_schema": schema,
	})
	id := jobID(t, run)

	result := callText(t, cs, "codex_result", map[string]any{"job": id, "wait_sec": 10})
	if strings.Contains(result, `"blob"`) {
		t.Fatalf("codex_result returned partial JSON:\n%s", tail(result, 300))
	}
	if !strings.Contains(result, "codex_output id="+id+":final offset=0 length=65536") {
		t.Fatalf("missing JSON paging hint:\n%s", result)
	}

	page := callText(t, cs, "codex_output", map[string]any{
		"id": id + ":final", "length": 65536,
	})
	if !strings.HasPrefix(page, `{"blob":"`) || !strings.Contains(page, "continue with codex_output") {
		t.Fatalf("unexpected first raw page: %q", tail(page, 300))
	}
	last := callText(t, cs, "codex_output", map[string]any{
		"id": id + ":final", "offset": 65536,
	})
	if !strings.Contains(last, `"}`) {
		t.Fatalf("last raw page missing JSON tail: %q", last)
	}
}

func TestFailedJob(t *testing.T) {
	t.Setenv("FAKE_CODEX_SCENARIO", "fail")
	cs := testServer(t, false)
	run := callText(t, cs, "codex_run", map[string]any{
		"prompt": "fail", "cwd": t.TempDir(), "sandbox": "read-only",
	})
	id := jobID(t, run)

	result := callText(t, cs, "codex_result", map[string]any{"job": id, "wait_sec": 10})
	for _, want := range []string{"failed", "exit=1", "error=fake turn failure", "partial answer", "[stderr]", "fake stderr"} {
		if !strings.Contains(result, want) {
			t.Fatalf("failed result missing %q:\n%s", want, result)
		}
	}

	status := callText(t, cs, "codex_status", map[string]any{"job": id})
	if !strings.Contains(status, "failed") || !strings.Contains(status, "stderr=") {
		t.Fatalf("unexpected failed status: %q", status)
	}
}

func TestCancelRunning(t *testing.T) {
	t.Setenv("FAKE_CODEX_SCENARIO", "sleep")
	t.Setenv("FAKE_CODEX_SLEEP_MS", "30000")
	cs := testServer(t, false)
	run := callText(t, cs, "codex_run", map[string]any{
		"prompt": "sleep", "cwd": t.TempDir(), "sandbox": "read-only", "timeout_sec": 60,
	})
	id := jobID(t, run)

	status := callText(t, cs, "codex_status", map[string]any{"job": id, "wait_sec": 1})
	if !strings.Contains(status, "running") && !strings.Contains(status, "queued") {
		t.Fatalf("unexpected running status: %q", status)
	}

	canceled := callText(t, cs, "codex_cancel", map[string]any{"job": id, "grace_sec": 2})
	if !strings.Contains(canceled, "canceled") && !strings.Contains(canceled, "cancel=requested") {
		t.Fatalf("unexpected cancel response: %q", canceled)
	}
	final := callText(t, cs, "codex_status", map[string]any{"job": id, "wait_sec": 3})
	if !strings.Contains(final, "canceled") {
		t.Fatalf("job did not reach canceled: %q", final)
	}
	result := callText(t, cs, "codex_result", map[string]any{"job": id})
	if !strings.Contains(result, "canceled") || !strings.Contains(result, "(no final message)") {
		t.Fatalf("unexpected canceled result: %q", result)
	}
}

func TestOutputPages(t *testing.T) {
	t.Setenv("FAKE_CODEX_SCENARIO", "success")
	cs := testServer(t, false)
	run := callText(t, cs, "codex_run", map[string]any{
		"prompt": "pages", "cwd": t.TempDir(), "sandbox": "read-only",
	})
	id := jobID(t, run)
	callText(t, cs, "codex_result", map[string]any{"job": id, "wait_sec": 10})

	first := callText(t, cs, "codex_output", map[string]any{
		"id": id + ":events", "length": 40,
	})
	if !strings.Contains(first, "continue with codex_output id="+id+":events offset=40") {
		t.Fatalf("missing events continuation: %q", first)
	}
	rest := callText(t, cs, "codex_output", map[string]any{
		"id": id + ":events", "offset": 40,
	})
	all := first + rest
	for _, want := range []string{"thread.started", "turn.completed"} {
		if !strings.Contains(all, want) {
			t.Fatalf("events missing %q:\n%s", want, all)
		}
	}

	final := callText(t, cs, "codex_output", map[string]any{"id": id})
	if !strings.Contains(final, "echo: pages") {
		t.Fatalf("final page = %q", final)
	}
	empty := callText(t, cs, "codex_output", map[string]any{"id": id + ":stderr"})
	if !strings.Contains(empty, "empty or not created yet") {
		t.Fatalf("stderr page = %q", empty)
	}
}

func TestToolErrors(t *testing.T) {
	cs := testServer(t, false)
	dir := t.TempDir()

	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"codex_run", map[string]any{"prompt": "  ", "cwd": dir}, "prompt"},
		{"codex_run", map[string]any{"prompt": "x", "cwd": filepath.Join(dir, "missing")}, "path"},
		{"codex_run", map[string]any{"prompt": "x", "cwd": dir, "sandbox": "danger-full-access"}, "disabled"},
		{"codex_run", map[string]any{"prompt": "x", "cwd": dir, "sandbox": "nope"}, "sandbox"},
		{"codex_run", map[string]any{"prompt": "x", "cwd": dir, "output_schema": "{"}, "valid JSON"},
		{"codex_run", map[string]any{"prompt": "x", "cwd": dir, "thread_id": "t", "profile": "p"}, "profile"},
		{"codex_run", map[string]any{"prompt": "x", "cwd": dir, "sandbox": "read-only", "add_dirs": []string{dir}}, "add_dirs"},
		{"codex_status", map[string]any{"job": "cx-999"}, "no job"},
		{"codex_result", map[string]any{"job": "cx-999"}, "no job"},
		{"codex_output", map[string]any{"id": "cx-999:final"}, "no job"},
		{"codex_output", map[string]any{"id": "cx-1:bogus"}, "unknown output kind"},
		{"codex_cancel", map[string]any{"job": "cx-999"}, "no job"},
		{"codex_list", map[string]any{"state": "bogus"}, "unknown state"},
	}
	for _, tc := range cases {
		res, err := callTool(t, cs, tc.tool, tc.args)
		msg := mustError(t, res, err)
		if !strings.Contains(strings.ToLower(msg), strings.ToLower(tc.want)) {
			t.Fatalf("%s %v: want %q in error, got %q", tc.tool, tc.args, tc.want, msg)
		}
	}
}

func TestFullAccessAllowed(t *testing.T) {
	t.Setenv("FAKE_CODEX_SCENARIO", "success")
	cs := testServer(t, true)
	run := callText(t, cs, "codex_run", map[string]any{
		"prompt": "yolo", "cwd": t.TempDir(), "sandbox": "danger-full-access",
	})
	if !strings.Contains(run, "sandbox=danger-full-access") {
		t.Fatalf("unexpected run: %q", run)
	}
	id := jobID(t, run)
	result := callText(t, cs, "codex_result", map[string]any{"job": id, "wait_sec": 10})
	if !strings.Contains(result, "exit=0") {
		t.Fatalf("full access job failed: %q", result)
	}
}

func TestResumePath(t *testing.T) {
	t.Setenv("FAKE_CODEX_SCENARIO", "success")
	cs := testServer(t, false)
	run := callText(t, cs, "codex_run", map[string]any{
		"prompt":    "continue",
		"cwd":       t.TempDir(),
		"sandbox":   "read-only",
		"thread_id": "0199f3c2-0000-7000-8000-000000000001",
	})
	if !strings.Contains(run, "resume=") {
		t.Fatalf("missing resume marker: %q", run)
	}
	id := jobID(t, run)
	result := callText(t, cs, "codex_result", map[string]any{"job": id, "wait_sec": 10})
	if !strings.Contains(result, "echo: continue") {
		t.Fatalf("resume result = %q", result)
	}
}

func TestNextOffset(t *testing.T) {
	text := "job=cx-1 ...\n---\nbody\n...[4 more bytes; continue with codex_result job=cx-1 offset=123]...\n"
	off, ok := nextOffset(text)
	if !ok || off != 123 {
		t.Fatalf("nextOffset = %d, %v", off, ok)
	}
	if _, ok := nextOffset("no note"); ok {
		t.Fatal("unexpected offset")
	}
}

func nextOffset(text string) (int, bool) {
	const marker = "continue with codex_result job="
	i := strings.LastIndex(text, marker)
	if i < 0 {
		return 0, false
	}
	rest := text[i:]
	j := strings.LastIndex(rest, "offset=")
	if j < 0 {
		return 0, false
	}
	rest = rest[j+len("offset="):]
	end := strings.IndexAny(rest, "] \n")
	if end >= 0 {
		rest = rest[:end]
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return n, true
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
