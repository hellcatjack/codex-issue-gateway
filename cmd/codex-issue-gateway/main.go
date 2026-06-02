package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/hellcatjack/codex-issue-gateway/internal/app"
	"github.com/hellcatjack/codex-issue-gateway/internal/server"
)

type serveFunc func(addr string, handler http.Handler) error

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stderr, http.ListenAndServe))
}

func run(ctx context.Context, args []string, stderr io.Writer, serve serveFunc) int {
	flags := flag.NewFlagSet("codex-issue-gateway", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to gateway config file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "missing --config")
		return 2
	}
	if serve == nil {
		serve = http.ListenAndServe
	}

	runtime, cleanup, err := app.LoadRuntime(ctx, *configPath)
	if err != nil {
		fmt.Fprintf(stderr, "failed to initialize gateway: %v\n", err)
		return 1
	}
	defer cleanup()
	stopWorkers := app.StartWorkerLoop(ctx, runtime)
	defer stopWorkers()

	if err := serve(runtime.Config.Server.Listen, server.New(runtime.ServerDependencies)); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(stderr, "gateway server stopped: %v\n", err)
		return 1
	}
	return 0
}
