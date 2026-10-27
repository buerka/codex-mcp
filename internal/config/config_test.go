package config

import (
	"path/filepath"
	"testing"
	"time"
)

var envNames = []string{
	"CODEX_BIN", "CODEX_MCP_JOBS_DIR", "CODEX_MCP_ROOT", "CODEX_MCP_TIMEOUT",
	"CODEX_MCP_MAX_CONCURRENCY", "CODEX_MCP_QUEUE_LIMIT", "CODEX_MCP_MAX_FINAL",
	"CODEX_MCP_MAX_JSON", "CODEX_MCP_MAX_DIAG", "CODEX_MCP_MAX_PROMPT",
	"CODEX_MCP_RETENTION", "CODEX_MCP_IGNORE_USER_CONFIG", "CODEX_MCP_ALLOW_NON_GIT",
	"CODEX_MCP_ALLOW_FULL_ACCESS",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range envNames {
		t.Setenv(name, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timeout != DefaultTimeout {
		t.Fatalf("timeout = %s", cfg.Timeout)
	}
	if cfg.MaxConcurrency != 1 || cfg.Retention != 100 {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.IgnoreUserConfig || cfg.AllowFullAccess || cfg.AllowNonGit {
		t.Fatalf("bool defaults = %+v", cfg)
	}
	if cfg.JobsDir == "" || !filepath.IsAbs(cfg.JobsDir) {
		t.Fatalf("jobs dir = %q", cfg.JobsDir)
	}
}

func TestLoadEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("CODEX_BIN", "/opt/codex")
	t.Setenv("CODEX_MCP_JOBS_DIR", "/tmp/jobs")
	t.Setenv("CODEX_MCP_TIMEOUT", "45m")
	t.Setenv("CODEX_MCP_MAX_CONCURRENCY", "2")
	t.Setenv("CODEX_MCP_IGNORE_USER_CONFIG", "1")
	t.Setenv("CODEX_MCP_ALLOW_FULL_ACCESS", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CodexBin != "/opt/codex" || cfg.JobsDir != "/tmp/jobs" {
		t.Fatalf("paths = %+v", cfg)
	}
	if cfg.Timeout != 45*time.Minute || cfg.MaxConcurrency != 2 {
		t.Fatalf("values = %+v", cfg)
	}
	if !cfg.IgnoreUserConfig || !cfg.AllowFullAccess {
		t.Fatalf("bools = %+v", cfg)
	}
}

func TestLoadInvalid(t *testing.T) {
	clearEnv(t)
	t.Setenv("CODEX_MCP_MAX_CONCURRENCY", "0")
	if _, err := Load(); err == nil {
		t.Fatal("zero concurrency accepted")
	}
	clearEnv(t)
	t.Setenv("CODEX_MCP_TIMEOUT", "nonsense")
	if _, err := Load(); err == nil {
		t.Fatal("bad duration accepted")
	}
	clearEnv(t)
	t.Setenv("CODEX_MCP_IGNORE_USER_CONFIG", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("bad bool accepted")
	}
}
