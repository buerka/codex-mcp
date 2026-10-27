package codex

import (
	"fmt"
	"os/exec"
)

// Locate finds the Codex CLI without guessing at app-bundled alpha binaries.
func Locate(explicit string) (string, error) {
	if explicit != "" {
		path, err := exec.LookPath(explicit)
		if err != nil {
			return "", fmt.Errorf("CODEX_BIN %q is not an executable: %w", explicit, err)
		}
		return path, nil
	}
	path, err := exec.LookPath("codex")
	if err != nil {
		return "", fmt.Errorf("codex CLI not found in PATH; install it or set CODEX_BIN")
	}
	return path, nil
}
