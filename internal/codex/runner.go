// Package codex runs the Codex CLI in non-interactive mode and parses its
// JSONL event stream.
package codex

import (
	"context"

	"github.com/buerka/codex-mcp/internal/proc"
)

// Usage is the token usage reported by Codex at the end of a turn.
type Usage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
}

// RunSpec is one `codex exec` invocation.
type RunSpec struct {
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
	OutputSchemaPath string
	FinalPath        string
	EventsPath       string
	StderrPath       string
}

// RunResult describes a finished (or failed to start) Codex process.
type RunResult struct {
	ExitCode    int
	ThreadID    string
	Usage       *Usage
	Failure     string
	Events      int
	BadLines    int
	LastMessage string
}

// Hooks lets the job manager terminate the process group on demand.
type Hooks struct {
	OnStart func(*proc.Process)
}

// Runner abstracts the Codex subprocess for tests.
type Runner interface {
	Run(ctx context.Context, spec RunSpec, hooks Hooks) (RunResult, error)
}
