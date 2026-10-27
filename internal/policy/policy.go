// Package policy validates sandbox modes and confines paths to an optional root.
package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	SandboxReadOnly       = "read-only"
	SandboxWorkspaceWrite = "workspace-write"
	SandboxFullAccess     = "danger-full-access"
)

// Policy decides which sandbox modes and filesystem paths are allowed.
type Policy struct {
	root            string
	allowFullAccess bool
}

// New resolves the optional root once so symlink checks use a stable path.
func New(root string, allowFullAccess bool) (*Policy, error) {
	p := &Policy{allowFullAccess: allowFullAccess}
	if root == "" {
		return p, nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("root %s: %w", root, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root %s is not a directory", real)
	}
	p.root = real
	return p, nil
}

// Root returns the resolved root, or "" when unrestricted.
func (p *Policy) Root() string { return p.root }

// Sandbox validates a requested sandbox, defaulting to workspace-write.
func (p *Policy) Sandbox(name string) (string, error) {
	switch name {
	case "":
		return SandboxWorkspaceWrite, nil
	case SandboxReadOnly, SandboxWorkspaceWrite:
		return name, nil
	case SandboxFullAccess:
		if !p.allowFullAccess {
			return "", fmt.Errorf("sandbox %q is disabled; set CODEX_MCP_ALLOW_FULL_ACCESS=1 to allow it", name)
		}
		return name, nil
	default:
		return "", fmt.Errorf("sandbox must be %q, %q or %q", SandboxReadOnly, SandboxWorkspaceWrite, SandboxFullAccess)
	}
}

// Dir resolves an existing directory and checks it against the root.
func (p *Policy) Dir(path string) (string, error) {
	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("working directory: %w", err)
		}
		path = cwd
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("path %s: %w", path, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("path %s: %w", path, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("path %s: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %s is not a directory", real)
	}
	if err := p.checkRoot(real); err != nil {
		return "", err
	}
	return real, nil
}

// File resolves an existing regular file and checks it against the root.
func (p *Policy) File(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("file path must not be empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("path %s: %w", path, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("path %s: %w", path, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("path %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("path %s is not a regular file", real)
	}
	if err := p.checkRoot(real); err != nil {
		return "", err
	}
	return real, nil
}

func (p *Policy) checkRoot(path string) error {
	if p.root == "" {
		return nil
	}
	rel, err := filepath.Rel(p.root, path)
	if err != nil {
		return fmt.Errorf("path %s is not inside root %s", path, p.root)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %s is outside root %s", path, p.root)
	}
	return nil
}
