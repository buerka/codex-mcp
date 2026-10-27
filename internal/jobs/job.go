package jobs

import (
	"context"
	"time"

	"github.com/buerka/codex-mcp/internal/codex"
	"github.com/buerka/codex-mcp/internal/proc"
)

// Paths are the per-job artifact files.
type Paths struct {
	Dir    string
	Final  string
	Events string
	Stderr string
	Schema string
}

type job struct {
	id        string
	state     State
	spec      codex.RunSpec
	timeout   time.Duration
	usedJSON  bool
	paths     Paths
	created   time.Time
	started   time.Time
	finished  time.Time
	exitCode  int
	threadID  string
	usage     *codex.Usage
	failure   string
	errMsg    string
	events    int
	handle    *proc.Process
	cancelFn  context.CancelFunc
	cancelReq bool
	timedOut  bool
	terminate bool
	grace     time.Duration
	done      chan struct{}
}

func (j *job) terminal() bool { return j.state.Terminal() }

// Snapshot is a race-free view of a job.
type Snapshot struct {
	ID              string
	State           State
	Cwd             string
	Sandbox         string
	Model           string
	ThreadID        string
	ExitCode        int
	Usage           *codex.Usage
	Failure         string
	Err             string
	Created         time.Time
	Started         time.Time
	Finished        time.Time
	Timeout         time.Duration
	Events          int
	FinalBytes      int64
	EventsBytes     int64
	StderrBytes     int64
	CancelRequested bool
	UsedJSON        bool
	Paths           Paths
}

// Terminal reports whether the job is finished.
func (s Snapshot) Terminal() bool { return s.State.Terminal() }

// Duration is execution time for started jobs, queue age for queued ones.
func (s Snapshot) Duration() time.Duration {
	if s.Created.IsZero() {
		return 0
	}
	start := s.Started
	if start.IsZero() {
		start = s.Created
	}
	end := time.Now()
	if !s.Finished.IsZero() {
		end = s.Finished
	}
	if end.Before(start) {
		return 0
	}
	return end.Sub(start)
}
