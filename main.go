// Command codex-mcp is a stdio MCP server that runs Codex CLI tasks as
// asynchronous jobs. Tool responses are plain text; final answers are paged
// sequentially and diagnostics are fetched on demand.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/buerka/codex-mcp/internal/codex"
	"github.com/buerka/codex-mcp/internal/config"
	"github.com/buerka/codex-mcp/internal/jobs"
	"github.com/buerka/codex-mcp/internal/policy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is overridden at build time via -ldflags="-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	bin, err := codex.Locate(cfg.CodexBin)
	if err != nil {
		fatal(err)
	}
	pol, err := policy.New(cfg.Root, cfg.AllowFullAccess)
	if err != nil {
		fatal(err)
	}
	mgr, err := jobs.New(cfg, &codex.ExecRunner{Bin: bin})
	if err != nil {
		fatal(err)
	}

	app := newApp(cfg, mgr, pol)
	server := newServer(app)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx, &mcp.StdioTransport{})
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	exitErr := error(nil)
	select {
	case err := <-errCh:
		exitErr = err
	case <-sigCh:
	}
	cancel()

	shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	mgr.Shutdown(shutdownCtx)
	stop()

	if exitErr != nil {
		fatal(exitErr)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "codex-mcp:", err)
	os.Exit(1)
}

// newServer registers all six tools. Split out of main so tests can drive the
// server over an in-memory transport.
func newServer(app *App) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "codex-mcp", Version: version}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "codex_run",
		Description: "start a Codex job in the background and return its id immediately",
	}, app.Run)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "codex_status",
		Description: "show one-line status for a Codex job, optionally waiting up to 30s",
	}, app.Status)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "codex_result",
		Description: "return a finished job's final answer; long text is paged with offset",
	}, app.Result)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "codex_output",
		Description: "page raw job output (final, events or stderr) by byte offset",
	}, app.Output)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "codex_cancel",
		Description: "cancel a queued or running Codex job",
	}, app.Cancel)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "codex_list",
		Description: "list recent Codex jobs, one per line",
	}, app.List)

	return server
}
