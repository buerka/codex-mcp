package codex

import (
	"encoding/json"
	"fmt"
)

// BuildArgs adapts a RunSpec to `codex exec` arguments. New sessions and
// resumptions accept different flag sets, so they are constructed separately.
func BuildArgs(spec RunSpec) []string {
	if spec.ThreadID != "" {
		return resumeArgs(spec)
	}
	args := []string{"exec", "--json", "--color", "never"}
	args = append(args, "-C", spec.Cwd)
	args = append(args, "-o", spec.FinalPath)
	if spec.IgnoreUserConfig {
		args = append(args, "--ignore-user-config")
	}
	if spec.SkipGitRepoCheck {
		args = append(args, "--skip-git-repo-check")
	}
	if spec.Ephemeral {
		args = append(args, "--ephemeral")
	}
	if spec.Model != "" {
		args = append(args, "-m", spec.Model)
	}
	if spec.Profile != "" {
		args = append(args, "-p", spec.Profile)
	}
	for _, img := range spec.Images {
		args = append(args, "-i", img)
	}
	if spec.OutputSchemaPath != "" {
		args = append(args, "--output-schema", spec.OutputSchemaPath)
	}
	for _, dir := range spec.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	switch spec.Sandbox {
	case "workspace-write":
		// --approve-for-me implies workspace-write and conflicts with -s.
		args = append(args, "--approve-for-me",
			"-c", fmt.Sprintf("sandbox_workspace_write.network_access=%t", spec.Network))
	case "danger-full-access":
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	case "read-only":
		args = append(args, "-s", "read-only")
	}

	return append(args, "-")
}

func resumeArgs(spec RunSpec) []string {
	args := []string{"exec", "--json", "--color", "never", "-o", spec.FinalPath}
	if spec.IgnoreUserConfig {
		args = append(args, "--ignore-user-config")
	}
	if spec.SkipGitRepoCheck {
		args = append(args, "--skip-git-repo-check")
	}
	if spec.Ephemeral {
		args = append(args, "--ephemeral")
	}
	if spec.Model != "" {
		args = append(args, "-m", spec.Model)
	}
	if spec.Sandbox == "workspace-write" {
		args = append(args, "--approve-for-me")
	}
	if spec.Sandbox == "danger-full-access" {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}

	args = append(args, "resume", "--all")
	for _, img := range spec.Images {
		args = append(args, "-i", img)
	}
	if spec.OutputSchemaPath != "" {
		args = append(args, "--output-schema", spec.OutputSchemaPath)
	}
	switch spec.Sandbox {
	case "workspace-write":
		args = append(args,
			"-c", `sandbox_mode="workspace-write"`,
			"-c", fmt.Sprintf("sandbox_workspace_write.network_access=%t", spec.Network))
		if len(spec.AddDirs) > 0 {
			// resume does not accept --add-dir; writable_roots is the config equivalent.
			if b, err := json.Marshal(spec.AddDirs); err == nil {
				args = append(args, "-c", "sandbox_workspace_write.writable_roots="+string(b))
			}
		}
	case "read-only":
		args = append(args, "-c", `sandbox_mode="read-only"`)
	}

	return append(args, spec.ThreadID, "-")
}
