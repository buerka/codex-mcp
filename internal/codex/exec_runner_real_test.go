package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealCodexResumeArgs is a local smoke test for the resume argv shape. It
// skips by default and never consumes model quota: an unknown thread id fails
// before a turn starts. Run with:
//
//	CODEX_MCP_REAL_SMOKE=1 CODEX_BIN=/opt/homebrew/bin/codex go test ./internal/codex -run TestRealCodexResumeArgs -v
func TestRealCodexResumeArgs(t *testing.T) {
	if os.Getenv("CODEX_MCP_REAL_SMOKE") != "1" {
		t.Skip("set CODEX_MCP_REAL_SMOKE=1 to run the real Codex CLI")
	}
	bin := os.Getenv("CODEX_BIN")
	if bin == "" {
		var err error
		bin, err = exec.LookPath("codex")
		if err != nil {
			t.Skip("codex not in PATH")
		}
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	dir := t.TempDir()
	extra := filepath.Join(dir, "extra")
	if err := os.Mkdir(extra, 0700); err != nil {
		t.Fatal(err)
	}
	schema := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schema, []byte(`{"type":"object"}`), 0600); err != nil {
		t.Fatal(err)
	}

	spec := RunSpec{
		Prompt:           "hi",
		Cwd:              dir,
		Sandbox:          "workspace-write",
		Network:          true,
		ThreadID:         "00000000-0000-0000-0000-000000000000",
		IgnoreUserConfig: true,
		SkipGitRepoCheck: true,
		AddDirs:          []string{extra},
		OutputSchemaPath: schema,
		FinalPath:        filepath.Join(dir, "final.txt"),
		EventsPath:       filepath.Join(dir, "events.jsonl"),
		StderrPath:       filepath.Join(dir, "stderr.log"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := (&ExecRunner{Bin: bin}).Run(ctx, spec, Hooks{})
	if err != nil {
		t.Fatalf("runner: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("unknown thread unexpectedly succeeded: %+v", res)
	}
	stderr, _ := os.ReadFile(spec.StderrPath)
	if !strings.Contains(string(stderr), "no rollout found") {
		t.Fatalf("stderr does not show a thread lookup failure:\n%s", stderr)
	}
}
