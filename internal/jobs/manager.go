package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/buerka/codex-mcp/internal/codex"
	"github.com/buerka/codex-mcp/internal/config"
	"github.com/buerka/codex-mcp/internal/proc"
)

// Request is a validated job submission from a tool handler.
type Request struct {
	Prompt           string
	Cwd              string
	Sandbox          string
	Network          bool
	Model            string
	Profile          string
	ThreadID         string
	Ephemeral        bool
	IgnoreUserConfig bool
	SkipGitRepoCheck bool
	AddDirs          []string
	Images           []string
	OutputSchema     []byte
	Timeout          time.Duration
}

// Manager owns the job registry, queue, workers and artifact retention.
type Manager struct {
	mu        sync.Mutex
	cond      *sync.Cond
	cfg       config.Config
	runner    codex.Runner
	jobs      map[string]*job
	order     []string
	queue     []*job
	cwdActive map[string]int
	active    int
	next      int
	shutting  bool
	rootCtx   context.Context
	stop      context.CancelFunc
	wg        sync.WaitGroup
}

// New prepares the jobs directory and starts the scheduler.
func New(cfg config.Config, runner codex.Runner) (*Manager, error) {
	if runner == nil {
		return nil, fmt.Errorf("runner is required")
	}
	if cfg.JobsDir == "" {
		return nil, fmt.Errorf("jobs directory is required")
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = config.DefaultWorkers
	}
	if cfg.QueueLimit <= 0 {
		cfg.QueueLimit = config.DefaultQueueLimit
	}
	if cfg.Retention <= 0 {
		cfg.Retention = config.DefaultRetention
	}
	if err := os.MkdirAll(cfg.JobsDir, 0700); err != nil {
		return nil, fmt.Errorf("create jobs directory %s: %w", cfg.JobsDir, err)
	}
	next, err := scanNext(cfg.JobsDir)
	if err != nil {
		return nil, fmt.Errorf("scan jobs directory %s: %w", cfg.JobsDir, err)
	}
	rootCtx, stop := context.WithCancel(context.Background())
	m := &Manager{
		cfg:       cfg,
		runner:    runner,
		jobs:      make(map[string]*job),
		cwdActive: make(map[string]int),
		next:      next,
		rootCtx:   rootCtx,
		stop:      stop,
	}
	m.cond = sync.NewCond(&m.mu)

	m.mu.Lock()
	remove := m.pruneLocked()
	m.mu.Unlock()
	for _, path := range remove {
		_ = os.RemoveAll(path)
	}

	go m.schedule()
	return m, nil
}

// Submit validates queue capacity, creates the job directory and enqueues the
// run. It returns immediately with a queued snapshot.
func (m *Manager) Submit(req Request) (Snapshot, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return Snapshot{}, fmt.Errorf("prompt must not be empty")
	}
	if req.Timeout <= 0 {
		req.Timeout = config.DefaultTimeout
	}
	if req.Cwd == "" {
		return Snapshot{}, fmt.Errorf("cwd must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shutting {
		return Snapshot{}, fmt.Errorf("server is shutting down")
	}
	if len(m.queue) >= m.cfg.QueueLimit {
		return Snapshot{}, fmt.Errorf("job queue is full (%d); wait for or cancel a job", m.cfg.QueueLimit)
	}

	id := fmt.Sprintf("cx-%d", m.next)
	m.next++
	paths := jobPaths(m.cfg.JobsDir, id)
	if err := os.MkdirAll(paths.Dir, 0700); err != nil {
		return Snapshot{}, fmt.Errorf("create job directory: %w", err)
	}
	if len(req.OutputSchema) > 0 {
		if err := os.WriteFile(paths.Schema, req.OutputSchema, 0600); err != nil {
			_ = os.RemoveAll(paths.Dir)
			return Snapshot{}, fmt.Errorf("write output schema: %w", err)
		}
	}

	spec := codex.RunSpec{
		Prompt:           req.Prompt,
		Cwd:              req.Cwd,
		Sandbox:          req.Sandbox,
		Network:          req.Network,
		Model:            req.Model,
		Profile:          req.Profile,
		ThreadID:         req.ThreadID,
		Ephemeral:        req.Ephemeral,
		IgnoreUserConfig: req.IgnoreUserConfig,
		SkipGitRepoCheck: req.SkipGitRepoCheck,
		AddDirs:          req.AddDirs,
		Images:           req.Images,
		FinalPath:        paths.Final,
		EventsPath:       paths.Events,
		StderrPath:       paths.Stderr,
	}
	if len(req.OutputSchema) > 0 {
		spec.OutputSchemaPath = paths.Schema
	}

	j := &job{
		id:       id,
		state:    Queued,
		spec:     spec,
		timeout:  req.Timeout,
		usedJSON: len(req.OutputSchema) > 0,
		paths:    paths,
		created:  time.Now(),
		done:     make(chan struct{}),
	}
	m.jobs[id] = j
	m.order = append(m.order, id)
	m.queue = append(m.queue, j)
	snap := m.snapshotLocked(j)
	m.cond.Signal()
	return snap, nil
}

// Snapshot returns the current state of one job.
func (m *Manager) Snapshot(id string) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Snapshot{}, false
	}
	return m.snapshotLocked(j), true
}

// Wait blocks until the job is terminal or the wait expires.
func (m *Manager) Wait(ctx context.Context, id string, d time.Duration) (Snapshot, bool) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return Snapshot{}, false
	}
	done := j.done
	snap := m.snapshotLocked(j)
	m.mu.Unlock()
	if d <= 0 || snap.Terminal() {
		return snap, true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
	return m.Snapshot(id)
}

// Cancel stops a queued or running job. Queued jobs stop immediately; running
// jobs get SIGTERM, then SIGKILL after grace.
func (m *Manager) Cancel(id string, grace time.Duration) (Snapshot, error) {
	if grace <= 0 {
		grace = defaultGrace
	}
	if grace > 30*time.Second {
		grace = 30 * time.Second
	}

	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return Snapshot{}, fmt.Errorf("no job %s", id)
	}
	if j.terminal() {
		snap := m.snapshotLocked(j)
		m.mu.Unlock()
		return snap, nil
	}
	j.grace = grace
	if j.state == Queued {
		m.removeQueuedLocked(j)
		j.state = Canceled
		j.finished = time.Now()
		close(j.done)
		snap := m.snapshotLocked(j)
		m.cond.Broadcast()
		m.mu.Unlock()
		m.prune()
		return snap, nil
	}
	j.cancelReq = true
	handle := j.handle
	cancelFn := j.cancelFn
	m.mu.Unlock()

	if cancelFn != nil {
		cancelFn()
	}
	if handle != nil {
		m.beginTerminate(j, handle, grace)
	}

	timer := time.NewTimer(grace + time.Second)
	defer timer.Stop()
	select {
	case <-j.done:
	case <-timer.C:
	}
	snap, ok := m.Snapshot(id)
	if !ok {
		return Snapshot{}, fmt.Errorf("job %s was removed", id)
	}
	return snap, nil
}

// List returns newest jobs first, optionally filtered by state.
func (m *Manager) List(limit int, state State) []Snapshot {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Snapshot, 0, limit)
	for i := len(m.order) - 1; i >= 0 && len(out) < limit; i-- {
		j, ok := m.jobs[m.order[i]]
		if !ok {
			continue
		}
		if state != "" && j.state != state {
			continue
		}
		out = append(out, m.snapshotLocked(j))
	}
	return out
}

// Shutdown cancels queued and running jobs and waits for workers.
func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	if m.shutting {
		m.mu.Unlock()
		return
	}
	m.shutting = true
	m.stop()
	now := time.Now()
	for _, j := range m.queue {
		if !j.terminal() {
			j.state = Canceled
			j.finished = now
			close(j.done)
		}
	}
	m.queue = nil
	m.cond.Broadcast()
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (m *Manager) schedule() {
	for {
		m.mu.Lock()
		for !m.shutting && !m.canStartLocked() {
			m.cond.Wait()
		}
		if m.shutting {
			m.mu.Unlock()
			return
		}
		j := m.popStartableLocked()
		if j == nil {
			m.mu.Unlock()
			continue
		}
		j.state = Starting
		j.started = time.Now()
		m.active++
		m.cwdActive[j.spec.Cwd]++
		m.wg.Add(1)
		m.mu.Unlock()
		go m.run(j)
	}
}

func (m *Manager) canStartLocked() bool {
	if m.active >= m.cfg.MaxConcurrency {
		return false
	}
	return m.startableIndexLocked() >= 0
}

func (m *Manager) startableIndexLocked() int {
	for i, j := range m.queue {
		if m.cwdActive[j.spec.Cwd] == 0 {
			return i
		}
	}
	return -1
}

func (m *Manager) popStartableLocked() *job {
	i := m.startableIndexLocked()
	if i < 0 {
		return nil
	}
	j := m.queue[i]
	m.queue = append(m.queue[:i], m.queue[i+1:]...)
	return j
}

func (m *Manager) run(j *job) {
	defer m.wg.Done()
	ctx, cancel := context.WithTimeout(m.rootCtx, j.timeout)

	m.mu.Lock()
	j.cancelFn = cancel
	if m.shutting || j.cancelReq {
		j.cancelReq = true
		j.state = Canceled
		j.finished = time.Now()
		close(j.done)
		m.active--
		m.cwdActive[j.spec.Cwd]--
		if m.cwdActive[j.spec.Cwd] == 0 {
			delete(m.cwdActive, j.spec.Cwd)
		}
		m.cond.Broadcast()
		m.mu.Unlock()
		cancel()
		m.prune()
		return
	}
	j.state = Running
	m.mu.Unlock()

	watchDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			m.onContextDone(j, ctx.Err())
		case <-watchDone:
		}
	}()

	res, runErr := m.runner.Run(ctx, j.spec, codex.Hooks{
		OnStart: func(p *proc.Process) { m.setHandle(j, p) },
	})
	ctxErr := ctx.Err()
	close(watchDone)
	m.finish(j, res, runErr, ctxErr)
	cancel()
}

func (m *Manager) onContextDone(j *job, err error) {
	m.mu.Lock()
	if j.terminal() {
		m.mu.Unlock()
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		j.timedOut = true
	} else {
		j.cancelReq = true
	}
	grace := j.grace
	if grace <= 0 {
		grace = defaultGrace
	}
	handle := j.handle
	m.mu.Unlock()
	if handle != nil {
		m.beginTerminate(j, handle, grace)
	}
}

func (m *Manager) setHandle(j *job, p *proc.Process) {
	m.mu.Lock()
	if j.terminal() {
		m.mu.Unlock()
		_ = p.Kill()
		return
	}
	j.handle = p
	need := j.cancelReq || j.timedOut || m.shutting
	grace := j.grace
	if grace <= 0 {
		grace = defaultGrace
	}
	m.mu.Unlock()
	if need {
		m.beginTerminate(j, p, grace)
	}
}

func (m *Manager) beginTerminate(j *job, p *proc.Process, grace time.Duration) {
	m.mu.Lock()
	if j.terminate || j.terminal() {
		m.mu.Unlock()
		return
	}
	j.terminate = true
	m.mu.Unlock()

	_ = p.Terminate()
	go func() {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-j.done:
		case <-timer.C:
			_ = p.Kill()
		}
	}()
}

func (m *Manager) finish(j *job, res codex.RunResult, runErr error, ctxErr error) {
	m.mu.Lock()
	if j.terminal() {
		m.mu.Unlock()
		return
	}
	j.exitCode = res.ExitCode
	if res.ThreadID != "" {
		j.threadID = res.ThreadID
	}
	if res.Usage != nil {
		u := *res.Usage
		j.usage = &u
	}
	j.events = res.Events
	j.failure = res.Failure

	timedOut := j.timedOut || (errors.Is(ctxErr, context.DeadlineExceeded) && (res.ExitCode != 0 || runErr != nil))
	switch {
	case timedOut:
		j.state = TimedOut
		if j.failure == "" || strings.Contains(strings.ToLower(j.failure), "signal") {
			j.failure = "timed out"
		}
	case j.cancelReq || errors.Is(ctxErr, context.Canceled) || m.shutting:
		j.cancelReq = true
		j.state = Canceled
	case runErr != nil:
		j.state = Failed
		j.errMsg = runErr.Error()
	case res.ExitCode != 0:
		j.state = Failed
		if j.failure == "" {
			j.failure = fmt.Sprintf("codex exited with code %d", res.ExitCode)
		}
	default:
		j.state = Succeeded
	}

	j.finished = time.Now()
	j.handle = nil
	m.active--
	m.cwdActive[j.spec.Cwd]--
	if m.cwdActive[j.spec.Cwd] == 0 {
		delete(m.cwdActive, j.spec.Cwd)
	}
	m.cond.Broadcast()
	remove := m.pruneLocked()
	m.mu.Unlock()

	for _, path := range remove {
		_ = os.RemoveAll(path)
	}
	// Close done only after retention cleanup so a completed result never
	// races with removal of the artifact it just produced.
	close(j.done)
}

func (m *Manager) snapshotLocked(j *job) Snapshot {
	s := Snapshot{
		ID:              j.id,
		State:           j.state,
		Cwd:             j.spec.Cwd,
		Sandbox:         j.spec.Sandbox,
		Model:           j.spec.Model,
		ThreadID:        j.threadID,
		ExitCode:        j.exitCode,
		Failure:         j.failure,
		Err:             j.errMsg,
		Created:         j.created,
		Started:         j.started,
		Finished:        j.finished,
		Timeout:         j.timeout,
		Events:          j.events,
		CancelRequested: j.cancelReq,
		UsedJSON:        j.usedJSON,
		Paths:           j.paths,
	}
	if j.usage != nil {
		u := *j.usage
		s.Usage = &u
	}
	if s.Created.IsZero() {
		s.Created = time.Now()
	}
	s.FinalBytes = fileSize(j.paths.Final)
	s.EventsBytes = fileSize(j.paths.Events)
	s.StderrBytes = fileSize(j.paths.Stderr)
	return s
}

func (m *Manager) removeQueuedLocked(j *job) {
	for i, q := range m.queue {
		if q == j {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			return
		}
	}
}

func (m *Manager) removeJobLocked(id string) {
	delete(m.jobs, id)
	for i, x := range m.order {
		if x == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			return
		}
	}
}

// pruneLocked applies the retention limit to the jobs directory. Active jobs
// are never removed, even if that leaves more than the configured count.
func (m *Manager) pruneLocked() []string {
	entries, err := os.ReadDir(m.cfg.JobsDir)
	if err != nil {
		return nil
	}
	type item struct {
		id string
		n  int
	}
	var items []item
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n, ok := jobNumber(e.Name())
		if !ok {
			continue
		}
		items = append(items, item{e.Name(), n})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].n < items[j].n })

	var remove []string
	for len(items) > m.cfg.Retention {
		it := items[0]
		items = items[1:]
		j, exists := m.jobs[it.id]
		if exists && !j.terminal() {
			continue
		}
		if exists {
			m.removeJobLocked(it.id)
		}
		remove = append(remove, filepath.Join(m.cfg.JobsDir, it.id))
	}
	return remove
}

func (m *Manager) prune() {
	m.mu.Lock()
	remove := m.pruneLocked()
	m.mu.Unlock()
	for _, path := range remove {
		_ = os.RemoveAll(path)
	}
}
