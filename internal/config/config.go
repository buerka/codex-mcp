// Package config loads codex-mcp settings from the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultTimeout    = 30 * time.Minute
	MaxTimeout        = 2 * time.Hour
	DefaultMaxFinal   = 32 * 1024
	DefaultMaxJSON    = 64 * 1024
	DefaultMaxDiag    = 8 * 1024
	DefaultMaxPrompt  = 64 * 1024
	DefaultRetention  = 100
	DefaultQueueLimit = 32
	DefaultWorkers    = 1
)

// Config is the fully resolved server configuration.
type Config struct {
	CodexBin         string
	JobsDir          string
	Root             string
	Timeout          time.Duration
	MaxConcurrency   int
	QueueLimit       int
	MaxFinal         int
	MaxJSON          int
	MaxDiag          int
	MaxPrompt        int
	Retention        int
	IgnoreUserConfig bool
	AllowNonGit      bool
	AllowFullAccess  bool
}

// Default returns the defaults, with the jobs directory under the user's home.
func Default() Config {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return Config{
		JobsDir:        filepath.Join(home, ".codex-mcp", "jobs"),
		Timeout:        DefaultTimeout,
		MaxConcurrency: DefaultWorkers,
		QueueLimit:     DefaultQueueLimit,
		MaxFinal:       DefaultMaxFinal,
		MaxJSON:        DefaultMaxJSON,
		MaxDiag:        DefaultMaxDiag,
		MaxPrompt:      DefaultMaxPrompt,
		Retention:      DefaultRetention,
	}
}

// Load reads the environment over the defaults and validates the result.
func Load() (Config, error) {
	c := Default()

	if v := os.Getenv("CODEX_BIN"); v != "" {
		c.CodexBin = expandHome(v)
	}
	if v := os.Getenv("CODEX_MCP_JOBS_DIR"); v != "" {
		c.JobsDir = expandHome(v)
	}
	if v := os.Getenv("CODEX_MCP_ROOT"); v != "" {
		c.Root = expandHome(v)
	}

	var err error
	if c.Timeout, err = envDuration("CODEX_MCP_TIMEOUT", c.Timeout); err != nil {
		return Config{}, err
	}
	if c.MaxConcurrency, err = envInt("CODEX_MCP_MAX_CONCURRENCY", c.MaxConcurrency); err != nil {
		return Config{}, err
	}
	if c.QueueLimit, err = envInt("CODEX_MCP_QUEUE_LIMIT", c.QueueLimit); err != nil {
		return Config{}, err
	}
	if c.MaxFinal, err = envInt("CODEX_MCP_MAX_FINAL", c.MaxFinal); err != nil {
		return Config{}, err
	}
	if c.MaxJSON, err = envInt("CODEX_MCP_MAX_JSON", c.MaxJSON); err != nil {
		return Config{}, err
	}
	if c.MaxDiag, err = envInt("CODEX_MCP_MAX_DIAG", c.MaxDiag); err != nil {
		return Config{}, err
	}
	if c.MaxPrompt, err = envInt("CODEX_MCP_MAX_PROMPT", c.MaxPrompt); err != nil {
		return Config{}, err
	}
	if c.Retention, err = envInt("CODEX_MCP_RETENTION", c.Retention); err != nil {
		return Config{}, err
	}
	if c.IgnoreUserConfig, err = envBool("CODEX_MCP_IGNORE_USER_CONFIG", c.IgnoreUserConfig); err != nil {
		return Config{}, err
	}
	if c.AllowNonGit, err = envBool("CODEX_MCP_ALLOW_NON_GIT", c.AllowNonGit); err != nil {
		return Config{}, err
	}
	if c.AllowFullAccess, err = envBool("CODEX_MCP_ALLOW_FULL_ACCESS", c.AllowFullAccess); err != nil {
		return Config{}, err
	}

	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate rejects values that would make the server behave unpredictably.
func (c Config) Validate() error {
	if c.JobsDir == "" {
		return fmt.Errorf("jobs directory must not be empty")
	}
	if c.Timeout <= 0 || c.Timeout > MaxTimeout {
		return fmt.Errorf("CODEX_MCP_TIMEOUT must be between 1s and %s", MaxTimeout)
	}
	if c.MaxConcurrency < 1 || c.MaxConcurrency > 16 {
		return fmt.Errorf("CODEX_MCP_MAX_CONCURRENCY must be between 1 and 16")
	}
	if c.QueueLimit < 1 || c.QueueLimit > 1024 {
		return fmt.Errorf("CODEX_MCP_QUEUE_LIMIT must be between 1 and 1024")
	}
	if c.MaxFinal < 1024 || c.MaxFinal > 16<<20 {
		return fmt.Errorf("CODEX_MCP_MAX_FINAL must be between 1024 and 16777216")
	}
	if c.MaxJSON < 1024 || c.MaxJSON > 16<<20 {
		return fmt.Errorf("CODEX_MCP_MAX_JSON must be between 1024 and 16777216")
	}
	if c.MaxDiag < 512 || c.MaxDiag > 1<<20 {
		return fmt.Errorf("CODEX_MCP_MAX_DIAG must be between 512 and 1048576")
	}
	if c.MaxPrompt < 1024 || c.MaxPrompt > 4<<20 {
		return fmt.Errorf("CODEX_MCP_MAX_PROMPT must be between 1024 and 4194304")
	}
	if c.Retention < 1 || c.Retention > 10000 {
		return fmt.Errorf("CODEX_MCP_RETENTION must be between 1 and 10000")
	}
	return nil
}

func envInt(name string, def int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not an integer", name, v)
	}
	return n, nil
}

func envBool(name string, def bool) (bool, error) {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if v == "" {
		return def, nil
	}
	switch v {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s: %q is not a boolean", name, v)
	}
}

func envDuration(name string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def, nil
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a duration (use seconds or 30m)", name, v)
	}
	return d, nil
}

func expandHome(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
