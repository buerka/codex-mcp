package jobs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/buerka/codex-mcp/internal/codex"
	"github.com/buerka/codex-mcp/internal/config"
)

type fakeRunner struct {
	mu      sync.Mutex
	specs   []codex.RunSpec
	delay   time.Duration
	fail    bool
	gate    chan struct{}
	started chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context, spec codex.RunSpec, hooks codex.Hooks) (codex.RunResult, error) {
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	f.mu.Unlock()
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return codex.RunResult{}, ctx.Err()
		}
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return codex.RunResult{}, ctx.Err()
		}
	}
	if f.gate != nil || f.delay > 0 {
		select {
		case <-ctx.Done():
			return codex.RunResult{}, ctx.Err()
		default:
		}
	}
	if err := os.WriteFile(spec.FinalPath, []byte("final answer"), 0600); err != nil {
		return codex.RunResult{}, err
	}
	if f.fail {
		return codex.RunResult{ExitCode: 1, Failure: "boom", ThreadID: "thread-x", Events: 2}, nil
	}
	return codex.RunResult{
		ExitCode: 0,
		ThreadID: "thread-x",
		Usage:    &codex.Usage{InputTokens: 10, OutputTokens: 2},
		Events:   2,
	}, nil
}

func testManager(t *testing.T, runner codex.Runner, tweak func(*config.Config)) *Manager {
	t.Helper()
	cfg := config.Default()
	cfg.JobsDir = t.TempDir()
	cfg.MaxConcurrency = 1
	cfg.QueueLimit = 4
	cfg.Retention = 100
	if tweak != nil {
		tweak(&cfg)
	}
	m, err := New(cfg, runner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		m.Shutdown(ctx)
	})
	return m
}

func TestSubmitAndSucceed(t *testing.T) {
	dir := t.TempDir()
	m := testManager(t, &fakeRunner{}, nil)
	snap, err := m.Submit(Request{Prompt: "hello", Cwd: dir, Sandbox: "workspace-write", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != Queued {
		t.Fatalf("state = %s", snap.State)
	}
	done, ok := m.Wait(context.Background(), snap.ID, 3*time.Second)
	if !ok {
		t.Fatal("job disappeared")
	}
	if done.State != Succeeded || done.ExitCode != 0 {
		t.Fatalf("final snapshot = %+v", done)
	}
	if done.ThreadID != "thread-x" || done.Usage == nil || done.Events != 2 {
		t.Fatalf("metadata missing: %+v", done)
	}
	b, err := os.ReadFile(done.Paths.Final)
	if err != nil || string(b) != "final answer" {
		t.Fatalf("final = %q, %v", b, err)
	}
}

func TestCancelQueued(t *testing.T) {
	dir := t.TempDir()
	gate := make(chan struct{})
	started := make(chan struct{}, 4)
	m := testManager(t, &fakeRunner{gate: gate, started: started}, nil)

	first, err := m.Submit(Request{Prompt: "one", Cwd: dir, Sandbox: "workspace-write", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first job did not start")
	}
	second, err := m.Submit(Request{Prompt: "two", Cwd: dir, Sandbox: "workspace-write", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := m.Cancel(second.ID, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.State != Canceled {
		t.Fatalf("canceled state = %s", canceled.State)
	}
	close(gate)
	if done, ok := m.Wait(context.Background(), first.ID, 2*time.Second); !ok || done.State != Succeeded {
		t.Fatalf("first job = %+v, %v", done, ok)
	}
}

func TestQueueLimit(t *testing.T) {
	dir := t.TempDir()
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	m := testManager(t, &fakeRunner{gate: gate, started: started}, func(cfg *config.Config) {
		cfg.QueueLimit = 1
	})
	first, err := m.Submit(Request{Prompt: "one", Cwd: dir, Sandbox: "workspace-write", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first job did not start")
	}
	if _, err := m.Submit(Request{Prompt: "two", Cwd: dir, Sandbox: "workspace-write", Timeout: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(Request{Prompt: "three", Cwd: dir, Sandbox: "workspace-write", Timeout: time.Minute}); err == nil {
		t.Fatal("third job accepted past the queue limit")
	}
	close(gate)
	if done, ok := m.Wait(context.Background(), first.ID, 2*time.Second); !ok || !done.Terminal() {
		t.Fatalf("first job = %+v", done)
	}
}

func TestTimeout(t *testing.T) {
	dir := t.TempDir()
	m := testManager(t, &fakeRunner{gate: make(chan struct{})}, nil)
	snap, err := m.Submit(Request{Prompt: "slow", Cwd: dir, Sandbox: "workspace-write", Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	done, ok := m.Wait(context.Background(), snap.ID, 3*time.Second)
	if !ok {
		t.Fatal("job disappeared")
	}
	if done.State != TimedOut {
		t.Fatalf("state = %s (%+v)", done.State, done)
	}
}

func TestRetention(t *testing.T) {
	dir := t.TempDir()
	m := testManager(t, &fakeRunner{}, func(cfg *config.Config) { cfg.Retention = 2 })
	var ids []string
	for i := 0; i < 3; i++ {
		snap, err := m.Submit(Request{Prompt: "job", Cwd: dir, Sandbox: "workspace-write", Timeout: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, snap.ID)
		if done, ok := m.Wait(context.Background(), snap.ID, 2*time.Second); !ok || !done.Terminal() {
			t.Fatalf("job did not finish: %+v", done)
		}
	}
	if _, ok := m.Snapshot(ids[0]); ok {
		t.Fatal("oldest job should be pruned")
	}
	entries, err := os.ReadDir(m.cfg.JobsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("retained %d dirs, want 2", len(entries))
	}
	if _, err := os.Stat(filepath.Join(m.cfg.JobsDir, ids[0])); !os.IsNotExist(err) {
		t.Fatalf("oldest dir still exists: %v", err)
	}
}

func TestShutdownCancelsRunning(t *testing.T) {
	dir := t.TempDir()
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	m := testManager(t, &fakeRunner{gate: gate, started: started}, nil)
	snap, err := m.Submit(Request{Prompt: "long", Cwd: dir, Sandbox: "workspace-write", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("job did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Shutdown(ctx)
	done, ok := m.Snapshot(snap.ID)
	if !ok {
		t.Fatal("job disappeared")
	}
	if done.State != Canceled {
		t.Fatalf("state after shutdown = %s", done.State)
	}
}
