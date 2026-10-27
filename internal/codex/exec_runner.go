package codex

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/buerka/codex-mcp/internal/proc"
)

// ExecRunner runs the real Codex CLI.
type ExecRunner struct {
	Bin string
}

// Run executes Codex with the prompt on stdin, copying JSONL events and stderr
// to the job directory while keeping only metadata in memory.
func (r *ExecRunner) Run(ctx context.Context, spec RunSpec, hooks Hooks) (RunResult, error) {
	var res RunResult
	if r.Bin == "" {
		return res, fmt.Errorf("codex binary is not configured")
	}
	if spec.FinalPath == "" || spec.EventsPath == "" || spec.StderrPath == "" {
		return res, fmt.Errorf("codex output paths are not configured")
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}

	stderrFile, err := os.OpenFile(spec.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return res, fmt.Errorf("open stderr log: %w", err)
	}
	eventsFile, err := os.OpenFile(spec.EventsPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		stderrFile.Close()
		return res, fmt.Errorf("open event log: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		stderrFile.Close()
		eventsFile.Close()
		return res, fmt.Errorf("create stdout pipe: %w", err)
	}

	cmd := exec.Command(r.Bin, BuildArgs(spec)...)
	cmd.Dir = spec.Cwd
	cmd.Stdin = strings.NewReader(spec.Prompt)
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrFile

	p, err := proc.Start(cmd)
	stdoutW.Close()
	if err != nil {
		stdoutR.Close()
		stderrFile.Close()
		eventsFile.Close()
		return res, fmt.Errorf("start codex: %w", err)
	}
	if hooks.OnStart != nil {
		hooks.OnStart(p)
	}

	eventsBuf := bufio.NewWriterSize(eventsFile, 64*1024)
	parseCh := make(chan ParseResult, 1)
	go func() {
		pr := ParseEvents(stdoutR, eventsBuf)
		stdoutR.Close()
		parseCh <- pr
	}()

	waitErr := cmd.Wait()
	var pr ParseResult
	select {
	case pr = <-parseCh:
	case <-time.After(2 * time.Second):
		// A lingering grandchild can keep the pipe open after Codex exits.
		// Stop waiting and keep whatever metadata was already parsed.
		stdoutR.Close()
		pr = <-parseCh
	}
	_ = eventsBuf.Flush()
	stderrFile.Close()
	eventsFile.Close()

	res.ThreadID = pr.ThreadID
	res.Usage = pr.Usage
	res.Failure = pr.Failure
	res.Events = pr.Events
	res.BadLines = pr.BadLines
	res.LastMessage = pr.LastMessage

	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			res.ExitCode = ee.ExitCode()
			if res.ExitCode < 0 {
				res.ExitCode = 1
				if res.Failure == "" {
					res.Failure = "codex terminated by signal"
				}
			}
		} else {
			return res, fmt.Errorf("wait for codex: %w", waitErr)
		}
	}

	// The final-message file is authoritative, but fall back to the last
	// agent_message event if an older CLI did not write it.
	if fileSize(spec.FinalPath) == 0 && pr.LastMessage != "" {
		if err := os.WriteFile(spec.FinalPath, []byte(pr.LastMessage), 0600); err != nil {
			return res, fmt.Errorf("write fallback final message: %w", err)
		}
	}
	if res.ExitCode == 0 && fileSize(spec.FinalPath) == 0 {
		return res, fmt.Errorf("codex exited 0 without a final message")
	}
	return res, nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
