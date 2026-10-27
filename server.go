package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/buerka/codex-mcp/internal/codex"
	"github.com/buerka/codex-mcp/internal/config"
	"github.com/buerka/codex-mcp/internal/jobs"
	"github.com/buerka/codex-mcp/internal/output"
	"github.com/buerka/codex-mcp/internal/policy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// App bundles the shared state behind the tool handlers.
type App struct {
	cfg    config.Config
	mgr    *jobs.Manager
	policy *policy.Policy
}

func newApp(cfg config.Config, mgr *jobs.Manager, pol *policy.Policy) *App {
	return &App{cfg: cfg, mgr: mgr, policy: pol}
}

// Text wraps a plain-text payload as the only tool content. Every response
// passes the redaction filter here as a final backstop.
func Text(s string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: output.Redact(s)}}}, nil, nil
}

func ToolError(err error) (*mcp.CallToolResult, any, error) {
	return nil, nil, fmt.Errorf("%s", output.Redact(err.Error()))
}

// RunIn starts a new Codex run or resumes an existing thread.
type RunIn struct {
	Prompt       string   `json:"prompt" jsonschema:"task for Codex (required)"`
	Cwd          string   `json:"cwd,omitempty" jsonschema:"working directory (default: server cwd or root)"`
	ThreadID     string   `json:"thread_id,omitempty" jsonschema:"resume this Codex thread id"`
	Model        string   `json:"model,omitempty" jsonschema:"model override"`
	Sandbox      string   `json:"sandbox,omitempty" jsonschema:"read-only, workspace-write (default) or danger-full-access (env-gated)"`
	Profile      string   `json:"profile,omitempty" jsonschema:"Codex config profile (new runs only)"`
	Network      *bool    `json:"network,omitempty" jsonschema:"workspace network access (default true)"`
	TimeoutSec   int      `json:"timeout_sec,omitempty" jsonschema:"timeout seconds (default 1800, max 7200)"`
	Ephemeral    bool     `json:"ephemeral,omitempty" jsonschema:"do not persist the Codex session"`
	Images       []string `json:"images,omitempty" jsonschema:"image files to attach"`
	AddDirs      []string `json:"add_dirs,omitempty" jsonschema:"extra writable directories"`
	OutputSchema string   `json:"output_schema,omitempty" jsonschema:"JSON Schema for the final response"`
}

// Run creates a job and returns immediately.
func (a *App) Run(ctx context.Context, req *mcp.CallToolRequest, in RunIn) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.Prompt) == "" {
		return ToolError(fmt.Errorf("prompt must not be empty"))
	}
	if len(in.Prompt) > a.cfg.MaxPrompt {
		return ToolError(fmt.Errorf("prompt is %d bytes, over the %d-byte limit", len(in.Prompt), a.cfg.MaxPrompt))
	}

	sandbox, err := a.policy.Sandbox(in.Sandbox)
	if err != nil {
		return ToolError(err)
	}
	cwdInput := in.Cwd
	if cwdInput == "" && a.policy.Root() != "" {
		cwdInput = a.policy.Root()
	}
	cwd, err := a.policy.Dir(cwdInput)
	if err != nil {
		return ToolError(err)
	}

	addDirs := make([]string, 0, len(in.AddDirs))
	for _, dir := range in.AddDirs {
		p, err := a.policy.Dir(dir)
		if err != nil {
			return ToolError(fmt.Errorf("add_dirs: %w", err))
		}
		addDirs = append(addDirs, p)
	}
	if sandbox == policy.SandboxReadOnly && len(addDirs) > 0 {
		return ToolError(fmt.Errorf("add_dirs requires workspace-write or danger-full-access"))
	}

	images := make([]string, 0, len(in.Images))
	for _, img := range in.Images {
		p, err := a.policy.File(img)
		if err != nil {
			return ToolError(fmt.Errorf("images: %w", err))
		}
		images = append(images, p)
	}

	if in.ThreadID != "" && in.Profile != "" {
		return ToolError(fmt.Errorf("profile is not supported when resuming a thread; set it on the original run"))
	}

	timeout := a.cfg.Timeout
	if in.TimeoutSec < 0 {
		return ToolError(fmt.Errorf("timeout_sec must not be negative"))
	}
	if in.TimeoutSec > 0 {
		timeout = time.Duration(in.TimeoutSec) * time.Second
	}
	if timeout > config.MaxTimeout {
		return ToolError(fmt.Errorf("timeout_sec must be at most %d", int(config.MaxTimeout/time.Second)))
	}

	var schema []byte
	if in.OutputSchema != "" {
		if len(in.OutputSchema) > 256*1024 {
			return ToolError(fmt.Errorf("output_schema is over the 262144-byte limit"))
		}
		if !json.Valid([]byte(in.OutputSchema)) {
			return ToolError(fmt.Errorf("output_schema is not valid JSON"))
		}
		schema = []byte(in.OutputSchema)
	}

	network := true
	if in.Network != nil {
		network = *in.Network
	}

	snap, err := a.mgr.Submit(jobs.Request{
		Prompt:           in.Prompt,
		Cwd:              cwd,
		Sandbox:          sandbox,
		Network:          network,
		Model:            in.Model,
		Profile:          in.Profile,
		ThreadID:         in.ThreadID,
		Ephemeral:        in.Ephemeral,
		IgnoreUserConfig: a.cfg.IgnoreUserConfig,
		SkipGitRepoCheck: a.cfg.AllowNonGit,
		AddDirs:          addDirs,
		Images:           images,
		OutputSchema:     schema,
		Timeout:          timeout,
	})
	if err != nil {
		return ToolError(err)
	}

	line := fmt.Sprintf("job=%s queued sandbox=%s", snap.ID, snap.Sandbox)
	if snap.Sandbox == policy.SandboxWorkspaceWrite {
		line += fmt.Sprintf(" network=%s", onOff(network))
	}
	line += fmt.Sprintf(" cwd=%s timeout=%s", shortPath(snap.Cwd, 80), formatDuration(snap.Timeout))
	if in.ThreadID != "" {
		line += " resume=" + shortThread(in.ThreadID)
	}
	return Text(line)
}

// StatusIn asks for a job's one-line status.
type StatusIn struct {
	Job     string `json:"job" jsonschema:"job id from codex_run"`
	WaitSec int    `json:"wait_sec,omitempty" jsonschema:"wait up to 30 seconds for completion"`
}

// Status returns a one-line status, optionally waiting.
func (a *App) Status(ctx context.Context, req *mcp.CallToolRequest, in StatusIn) (*mcp.CallToolResult, any, error) {
	if in.Job == "" {
		return ToolError(fmt.Errorf("job must not be empty"))
	}
	wait := time.Duration(clamp(in.WaitSec, 0, 30)) * time.Second
	snap, ok := a.mgr.Wait(ctx, in.Job, wait)
	if !ok {
		return ToolError(fmt.Errorf("no job %s", in.Job))
	}
	return Text(statusLine(snap))
}

// ResultIn asks for a job's final answer.
type ResultIn struct {
	Job      string `json:"job" jsonschema:"job id from codex_run"`
	WaitSec  int    `json:"wait_sec,omitempty" jsonschema:"wait up to 120 seconds for completion"`
	Offset   int    `json:"offset,omitempty" jsonschema:"byte offset for the next text page"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"page size in bytes (default 32768, max 262144)"`
}

// Result returns the final message, with sequential paging for long text.
func (a *App) Result(ctx context.Context, req *mcp.CallToolRequest, in ResultIn) (*mcp.CallToolResult, any, error) {
	if in.Job == "" {
		return ToolError(fmt.Errorf("job must not be empty"))
	}
	if in.Offset < 0 {
		return ToolError(fmt.Errorf("offset must not be negative"))
	}
	wait := time.Duration(clamp(in.WaitSec, 0, 120)) * time.Second
	snap, ok := a.mgr.Wait(ctx, in.Job, wait)
	if !ok {
		return ToolError(fmt.Errorf("no job %s", in.Job))
	}
	if !snap.Terminal() {
		return Text(statusLine(snap) + "\njob is still running; call codex_result again with wait_sec")
	}
	text, err := a.renderResult(snap, in.Offset, in.MaxBytes)
	if err != nil {
		return ToolError(err)
	}
	return Text(text)
}

// OutputIn pages through raw job artifacts.
type OutputIn struct {
	ID     string `json:"id" jsonschema:"<job>:final, <job>:events or <job>:stderr (default final)"`
	Offset int    `json:"offset,omitempty" jsonschema:"byte offset to start from"`
	Length int    `json:"length,omitempty" jsonschema:"bytes to return (default 32768, max 65536)"`
}

// Output pages through a job's final, events or stderr file.
func (a *App) Output(ctx context.Context, req *mcp.CallToolRequest, in OutputIn) (*mcp.CallToolResult, any, error) {
	jobID, kind, err := splitOutputID(in.ID)
	if err != nil {
		return ToolError(err)
	}
	if in.Offset < 0 {
		return ToolError(fmt.Errorf("offset must not be negative"))
	}
	snap, ok := a.mgr.Snapshot(jobID)
	if !ok {
		return ToolError(fmt.Errorf("no job %s", jobID))
	}
	var path string
	switch kind {
	case "final":
		path = snap.Paths.Final
	case "events":
		path = snap.Paths.Events
	case "stderr":
		path = snap.Paths.Stderr
	}
	size := output.Size(path)
	if size == 0 {
		return Text(fmt.Sprintf("job=%s state=%s; %s output is empty or not created yet", snap.ID, snap.State, kind))
	}
	max := in.Length
	if max <= 0 {
		max = 32 * 1024
	}
	if max > 64*1024 {
		max = 64 * 1024
	}
	body, total, more, err := output.ReadPage(path, in.Offset, max)
	if err != nil {
		return ToolError(fmt.Errorf("read %s output: %w", kind, err))
	}
	if body == "" {
		return Text(fmt.Sprintf("(offset %d is at or past the end; %s is %d bytes)", in.Offset, kind, total))
	}
	text := output.Redact(body)
	if more {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += fmt.Sprintf("...[%d more bytes; continue with codex_output id=%s:%s offset=%d]...\n",
			total-int64(in.Offset+len(body)), jobID, kind, in.Offset+len(body))
	}
	return Text(text)
}

// CancelIn stops a job.
type CancelIn struct {
	Job      string `json:"job" jsonschema:"job id from codex_run"`
	GraceSec int    `json:"grace_sec,omitempty" jsonschema:"seconds between SIGTERM and SIGKILL (default 5, max 30)"`
}

// Cancel stops a queued or running job.
func (a *App) Cancel(ctx context.Context, req *mcp.CallToolRequest, in CancelIn) (*mcp.CallToolResult, any, error) {
	if in.Job == "" {
		return ToolError(fmt.Errorf("job must not be empty"))
	}
	graceSec := 5
	if in.GraceSec > 0 {
		graceSec = in.GraceSec
	}
	if graceSec > 30 {
		graceSec = 30
	}
	grace := time.Duration(graceSec) * time.Second
	snap, err := a.mgr.Cancel(in.Job, grace)
	if err != nil {
		return ToolError(err)
	}
	return Text(statusLine(snap))
}

// ListIn lists recent jobs.
type ListIn struct {
	Limit int    `json:"limit,omitempty" jsonschema:"max jobs to return (default 20, max 100)"`
	State string `json:"state,omitempty" jsonschema:"filter: queued, running, succeeded, failed, timed_out or canceled"`
}

// List prints one line per job, newest first.
func (a *App) List(ctx context.Context, req *mcp.CallToolRequest, in ListIn) (*mcp.CallToolResult, any, error) {
	state, err := jobs.ParseState(in.State)
	if err != nil {
		return ToolError(err)
	}
	snaps := a.mgr.List(in.Limit, state)
	if len(snaps) == 0 {
		return Text("(no jobs)")
	}
	var sb strings.Builder
	for _, snap := range snaps {
		sb.WriteString(listLine(snap))
		sb.WriteByte('\n')
	}
	return Text(strings.TrimRight(sb.String(), "\n"))
}

func (a *App) renderResult(s jobs.Snapshot, offset, maxBytes int) (string, error) {
	max := maxBytes
	if max <= 0 {
		if s.UsedJSON {
			max = a.cfg.MaxJSON
		} else {
			max = a.cfg.MaxFinal
		}
	}
	if max < 512 {
		max = 512
	}
	if max > 256*1024 {
		max = 256 * 1024
	}

	var sb strings.Builder
	sb.WriteString(resultHeader(s, offset))
	sb.WriteString("\n---\n")

	if s.UsedJSON {
		size := output.Size(s.Paths.Final)
		switch {
		case size == 0:
			sb.WriteString("(no final message)")
		case size <= int64(max) && offset == 0:
			b, err := os.ReadFile(s.Paths.Final)
			if err != nil {
				return "", fmt.Errorf("read final message: %w", err)
			}
			sb.WriteString(output.Redact(string(b)))
		case size <= int64(max):
			fmt.Fprintf(&sb, "JSON output is %d bytes and fits in one page; call codex_result job=%s without offset", size, s.ID)
		default:
			page := max
			if page > 64*1024 {
				page = 64 * 1024
			}
			fmt.Fprintf(&sb, "JSON output is %d bytes, over the %d-byte inline limit; fetch it in order with codex_output id=%s:final offset=0 length=%d (raw byte pages)",
				size, max, s.ID, page)
		}
	} else {
		body, size, more, err := output.ReadPage(s.Paths.Final, offset, max)
		if err != nil {
			if os.IsNotExist(err) {
				sb.WriteString("(no final message)")
			} else {
				return "", fmt.Errorf("read final message: %w", err)
			}
		} else if body == "" && size == 0 {
			sb.WriteString("(no final message)")
		} else if body == "" {
			fmt.Fprintf(&sb, "(offset %d is at or past the end; final is %d bytes)", offset, size)
		} else {
			sb.WriteString(output.Redact(body))
			if more {
				if !strings.HasSuffix(sb.String(), "\n") {
					sb.WriteString("\n")
				}
				fmt.Fprintf(&sb, "...[%d more bytes; continue with codex_result job=%s offset=%d]...\n",
					size-int64(offset+len(body)), s.ID, offset+len(body))
			}
		}
	}

	if s.State == jobs.Failed || s.State == jobs.TimedOut {
		if s.StderrBytes > 0 {
			stderr, err := output.HeadTail(s.Paths.Stderr, a.cfg.MaxDiag, s.ID+":stderr")
			if err == nil && strings.TrimSpace(stderr) != "" {
				sb.WriteString("\n[stderr]\n")
				sb.WriteString(output.Redact(stderr))
				if !strings.HasSuffix(stderr, "\n") {
					sb.WriteString("\n")
				}
			}
		}
	}
	return sb.String(), nil
}

func resultHeader(s jobs.Snapshot, offset int) string {
	parts := []string{"job=" + s.ID}
	switch s.State {
	case jobs.Succeeded:
		parts = append(parts, fmt.Sprintf("exit=%d", s.ExitCode))
	case jobs.Failed:
		parts = append(parts, "failed", fmt.Sprintf("exit=%d", s.ExitCode))
	case jobs.TimedOut:
		parts = append(parts, "timed_out")
	case jobs.Canceled:
		parts = append(parts, "canceled")
	default:
		parts = append(parts, string(s.State))
	}
	parts = append(parts, formatDuration(s.Duration()))
	if s.ThreadID != "" {
		parts = append(parts, "thread="+shortThread(s.ThreadID))
	}
	if s.Usage != nil {
		parts = append(parts, "usage="+usageString(s.Usage))
	}
	if s.State == jobs.TimedOut {
		parts = append(parts, "timeout="+formatDuration(s.Timeout))
	}
	if s.State == jobs.Failed || s.State == jobs.TimedOut {
		reason := s.Failure
		if reason == "" {
			reason = s.Err
		}
		if reason != "" {
			parts = append(parts, "error="+output.Redact(oneLine(reason, 180)))
		}
	}
	if offset > 0 {
		parts = append(parts, fmt.Sprintf("offset=%d", offset))
	}
	return strings.Join(parts, " ")
}

func statusLine(s jobs.Snapshot) string {
	parts := []string{"job=" + s.ID, string(s.State)}
	if s.State == jobs.Succeeded || s.State == jobs.Failed {
		parts = append(parts, fmt.Sprintf("exit=%d", s.ExitCode))
	}
	parts = append(parts, formatDuration(s.Duration()))
	if s.ThreadID != "" {
		parts = append(parts, "thread="+shortThread(s.ThreadID))
	}
	if s.Usage != nil {
		parts = append(parts, "usage="+usageString(s.Usage))
	}
	if s.State == jobs.TimedOut {
		parts = append(parts, "timeout="+formatDuration(s.Timeout))
	}
	if s.State == jobs.Failed || s.State == jobs.TimedOut {
		reason := s.Failure
		if reason == "" {
			reason = s.Err
		}
		if reason != "" {
			parts = append(parts, "error="+output.Redact(oneLine(reason, 160)))
		}
	}
	if s.CancelRequested && !s.Terminal() {
		parts = append(parts, "cancel=requested")
	}
	if s.Events > 0 {
		parts = append(parts, fmt.Sprintf("events=%d", s.Events))
	}
	if s.StderrBytes > 0 && s.State != jobs.Succeeded {
		parts = append(parts, "stderr="+humanBytes(s.StderrBytes))
	}
	return strings.Join(parts, " ")
}

func listLine(s jobs.Snapshot) string {
	parts := []string{s.ID, string(s.State), formatDuration(s.Duration())}
	if s.State == jobs.Succeeded || s.State == jobs.Failed {
		parts = append(parts, fmt.Sprintf("exit=%d", s.ExitCode))
	}
	if s.ThreadID != "" {
		parts = append(parts, "thread="+shortThread(s.ThreadID))
	}
	parts = append(parts, shortPath(s.Cwd, 64))
	return strings.Join(parts, " ")
}

func splitOutputID(id string) (jobID, kind string, err error) {
	if id == "" {
		return "", "", fmt.Errorf("id must not be empty")
	}
	parts := strings.Split(id, ":")
	if len(parts) > 2 || parts[0] == "" {
		return "", "", fmt.Errorf("id must look like cx-7, cx-7:final, cx-7:events or cx-7:stderr")
	}
	jobID = parts[0]
	kind = "final"
	if len(parts) == 2 {
		kind = parts[1]
	}
	switch kind {
	case "final", "events", "stderr":
		return jobID, kind, nil
	default:
		return "", "", fmt.Errorf("unknown output kind %q; want final, events or stderr", kind)
	}
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	if d < time.Hour {
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	}
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

func humanBytes(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + "B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n)
	for _, unit := range units {
		v /= 1024
		if v < 1024 || unit == "TB" {
			return trimZero(strconv.FormatFloat(v, 'f', 1, 64)) + unit
		}
	}
	return strconv.FormatInt(n, 10) + "B"
}

func usageString(u *codex.Usage) string {
	return humanCount(u.InputTokens) + "/" + humanCount(u.OutputTokens)
}

func humanCount(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return trimZero(strconv.FormatFloat(float64(n)/1000, 'f', 1, 64)) + "k"
	default:
		return trimZero(strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64)) + "M"
	}
}

func trimZero(s string) string {
	return strings.TrimSuffix(s, ".0")
}

func shortThread(id string) string {
	id = singleLine(id)
	if utf8.RuneCountInString(id) <= 12 {
		return id
	}
	r := []rune(id)
	return string(r[:12]) + "…"
}

func shortPath(path string, max int) string {
	path = singleLine(path)
	if max <= 1 {
		max = 2
	}
	r := []rune(path)
	if len(r) <= max {
		return path
	}
	return "…" + string(r[len(r)-max+1:])
}

// singleLine keeps metadata on one physical line without collapsing spaces.
func singleLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		default:
			return r
		}
	}, s)
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if max <= 1 || len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
