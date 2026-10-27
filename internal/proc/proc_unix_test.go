//go:build !windows

package proc

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestTerminateKillsWholeGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30 & wait")
	p, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if err := p.Terminate(); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = p.Kill()
		t.Fatal("process group did not exit after SIGTERM")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(-pgid, 0)
		if err == syscall.ESRCH {
			return
		}
		if time.Now().After(deadline) {
			_ = p.Kill()
			t.Fatalf("process group %d still exists: %v", pgid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
