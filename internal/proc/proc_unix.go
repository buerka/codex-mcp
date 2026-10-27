//go:build !windows

package proc

import (
	"errors"
	"os/exec"
	"syscall"
)

// Process owns a child process group so cancellation reaches grandchildren.
type Process struct {
	cmd *exec.Cmd
}

// Start puts the child in its own process group and starts it.
func Start(cmd *exec.Cmd) (*Process, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Process{cmd: cmd}, nil
}

// Terminate asks the whole process group to exit with SIGTERM.
func (p *Process) Terminate() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(p.cmd.Process.Pid)
	if err != nil {
		err = p.cmd.Process.Signal(syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	err = syscall.Kill(-pgid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// Kill force-kills the whole process group with SIGKILL.
func (p *Process) Kill() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(p.cmd.Process.Pid)
	if err != nil {
		err = p.cmd.Process.Kill()
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	err = syscall.Kill(-pgid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
