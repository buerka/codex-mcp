// Package jobs schedules Codex runs and stores their state and output files.
package jobs

import "fmt"

// State is the lifecycle state of a Codex job.
type State string

const (
	Queued    State = "queued"
	Starting  State = "starting"
	Running   State = "running"
	Succeeded State = "succeeded"
	Failed    State = "failed"
	TimedOut  State = "timed_out"
	Canceled  State = "canceled"
)

// Terminal reports whether no further state transition is possible.
func (s State) Terminal() bool {
	switch s {
	case Succeeded, Failed, TimedOut, Canceled:
		return true
	}
	return false
}

// ParseState validates a state filter from a tool argument.
func ParseState(s string) (State, error) {
	switch State(s) {
	case "", Queued, Starting, Running, Succeeded, Failed, TimedOut, Canceled:
		return State(s), nil
	}
	return "", fmt.Errorf("unknown state %q", s)
}
