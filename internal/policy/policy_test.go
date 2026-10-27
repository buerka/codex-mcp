package policy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSandbox(t *testing.T) {
	p, err := New("", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Sandbox("")
	if err != nil || got != SandboxWorkspaceWrite {
		t.Fatalf("default sandbox = %q, %v", got, err)
	}
	if _, err := p.Sandbox("danger-full-access"); err == nil {
		t.Fatal("full access should be gated")
	}
	allowed, err := New("", true)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := allowed.Sandbox("danger-full-access"); err != nil || got != SandboxFullAccess {
		t.Fatalf("gated sandbox = %q, %v", got, err)
	}
	if _, err := p.Sandbox("root"); err == nil {
		t.Fatal("unknown sandbox should fail")
	}
}

func TestRootContainment(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "repo")
	outside := t.TempDir()
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := New(root, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Dir(inside)
	if err != nil {
		t.Fatalf("inside dir rejected: %v", err)
	}
	if got == "" {
		t.Fatal("empty resolved dir")
	}
	if _, err := p.Dir(outside); err == nil {
		t.Fatal("outside dir accepted")
	}
	if _, err := p.Dir(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing dir accepted")
	}
}

func TestSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs extra privileges on windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	p, err := New(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Dir(link); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func TestFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "image.png")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := New(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.File(path); err != nil {
		t.Fatalf("file rejected: %v", err)
	}
	if _, err := p.File(root); err == nil {
		t.Fatal("directory accepted as file")
	}
}
