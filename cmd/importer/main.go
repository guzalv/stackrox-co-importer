package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/stackrox/co-importer/internal/acs"
	"github.com/stackrox/co-importer/internal/config"
	"github.com/stackrox/co-importer/internal/run"
	"github.com/stackrox/co-importer/internal/status"
)

// Set by GoReleaser via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("stackrox-co-importer %s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}
	os.Exit(runMain())
}

func runMain() int {
	printer := status.New()

	// Parse configuration from CLI args and environment.
	cfg, err := config.Parse(os.Args[1:], os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return config.ExitConfigError
	}

	// IMP-CLI-029: --list-ssbs skips ACS entirely.
	if cfg.ListSSBs {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		return run.ListSSBs(ctx, cfg, os.Stdout)
	}

	// Create ACS HTTP client.
	acsClient, err := acs.NewHTTPClient(acs.HTTPClientConfig{
		Endpoint:           cfg.Endpoint,
		Token:              cfg.Token,
		Username:           cfg.Username,
		Password:           cfg.Password,
		CACertFile:         cfg.CACertFile,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		RequestTimeout:     cfg.RequestTimeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to create ACS client: %v\n", err)
		return config.ExitConfigError
	}

	// Set up context with signal handling.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Preflight check: probe ACS auth (IMP-CLI-015).
	printer.Stage("Preflight", "checking ACS connectivity and auth")
	if err := acsClient.Preflight(ctx); err != nil {
		printer.Fail(fmt.Sprintf("ACS preflight failed: %v", err))
		return config.ExitConfigError
	}
	printer.OK("ACS preflight passed")

	// Create and run the pipeline.
	runner := run.NewRunner(cfg, acsClient)
	return runner.Run(ctx)
}
