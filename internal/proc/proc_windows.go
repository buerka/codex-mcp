//go:build windows

package proc

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNewProcessGroup = 0x00000200

// Process owns a child process on Windows. taskkill /T reaches descendants.
type Process struct {
	cmd *exec.Cmd
	pid int
}

// Start puts the child in a new process group and starts it.
func Start(cmd *exec.Cmd) (*Process, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Process{cmd: cmd, pid: cmd.Process.Pid}, nil
}

// Terminate ends the process tree. Windows has no SIGTERM equivalent here.
func (p *Process) Terminate() error { return p.taskkill() }

// Kill ends the process tree.
func (p *Process) Kill() error { return p.taskkill() }

func (p *Process) taskkill() error {
	if p == nil || p.pid == 0 {
		return nil
	}
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.pid)).Run()
}
