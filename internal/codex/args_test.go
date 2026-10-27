package codex

import (
	"reflect"
	"testing"
)

func TestBuildArgsNewWorkspaceWrite(t *testing.T) {
	args := BuildArgs(RunSpec{
		Prompt:    "fix it",
		Cwd:       "/repo",
		Sandbox:   "workspace-write",
		Network:   true,
		FinalPath: "/jobs/cx-1/final.txt",
	})
	if args[0] != "exec" || args[len(args)-1] != "-" {
		t.Fatalf("unexpected argv shape: %v", args)
	}
	if !contains(args, "--json") || !contains(args, "--color") || !contains(args, "never") {
		t.Fatalf("missing json/color flags: %v", args)
	}
	if !contains(args, "--approve-for-me") {
		t.Fatalf("workspace-write should use --approve-for-me: %v", args)
	}
	if contains(args, "-s") {
		t.Fatalf("--approve-for-me conflicts with -s: %v", args)
	}
	if !containsPair(args, "-c", "sandbox_workspace_write.network_access=true") {
		t.Fatalf("missing network override: %v", args)
	}
	if !containsPair(args, "-C", "/repo") {
		t.Fatalf("missing -C: %v", args)
	}
	if !containsPair(args, "-o", "/jobs/cx-1/final.txt") {
		t.Fatalf("missing -o: %v", args)
	}
}

func TestBuildArgsNewReadOnly(t *testing.T) {
	args := BuildArgs(RunSpec{Cwd: "/repo", Sandbox: "read-only", Network: true, FinalPath: "/f"})
	if !containsPair(args, "-s", "read-only") {
		t.Fatalf("missing read-only sandbox: %v", args)
	}
	if contains(args, "--approve-for-me") {
		t.Fatalf("read-only should not approve: %v", args)
	}
}

func TestBuildArgsFullAccess(t *testing.T) {
	args := BuildArgs(RunSpec{Cwd: "/repo", Sandbox: "danger-full-access", FinalPath: "/f"})
	if !contains(args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("missing yolo flag: %v", args)
	}
}

func TestBuildArgsResume(t *testing.T) {
	args := BuildArgs(RunSpec{
		Cwd:       "/repo",
		Sandbox:   "workspace-write",
		Network:   false,
		ThreadID:  "thread-1",
		AddDirs:   []string{"/extra"},
		Images:    []string{"/tmp/a.png"},
		FinalPath: "/jobs/cx-2/final.txt",
	})
	resume := indexOf(args, "resume")
	if resume < 0 {
		t.Fatalf("missing resume: %v", args)
	}
	if !containsSequence(args, "resume", "--all") {
		t.Fatalf("missing resume --all: %v", args)
	}
	if len(args) < 2 || args[len(args)-2] != "thread-1" || args[len(args)-1] != "-" {
		t.Fatalf("thread id and stdin marker must be last: %v", args)
	}
	if contains(args, "-C") || contains(args, "-p") {
		t.Fatalf("resume must not use -C or -p: %v", args)
	}
	if contains(args, "-s") {
		t.Fatalf("resume must not use -s: %v", args)
	}
	if !containsPair(args, "-c", `sandbox_mode="workspace-write"`) {
		t.Fatalf("missing sandbox_mode override: %v", args)
	}
	if !containsPair(args, "-c", "sandbox_workspace_write.network_access=false") {
		t.Fatalf("missing network override: %v", args)
	}
	if !containsPair(args, "-c", `sandbox_workspace_write.writable_roots=["/extra"]`) {
		t.Fatalf("missing writable_roots: %v", args)
	}
	if !containsPair(args, "-i", "/tmp/a.png") {
		t.Fatalf("image must be passed to resume: %v", args)
	}
	if args[len(args)-1] != "-" {
		t.Fatalf("prompt stdin marker must be last: %v", args)
	}
}

func TestBuildArgsDeterministic(t *testing.T) {
	spec := RunSpec{Cwd: "/repo", Sandbox: "workspace-write", FinalPath: "/f", AddDirs: []string{"/a", "/b"}}
	first := BuildArgs(spec)
	second := BuildArgs(spec)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("argv is not deterministic:\n%v\n%v", first, second)
	}
	if !containsPair(first, "--add-dir", "/a") || !containsPair(first, "--add-dir", "/b") {
		t.Fatalf("missing add-dir flags: %v", first)
	}
}

func contains(args []string, want string) bool { return indexOf(args, want) >= 0 }

func containsPair(args []string, a, b string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == a && args[i+1] == b {
			return true
		}
	}
	return false
}

func containsSequence(args []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		ok := true
		for j, want := range seq {
			if args[i+j] != want {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func indexOf(args []string, want string) int {
	for i, arg := range args {
		if arg == want {
			return i
		}
	}
	return -1
}
