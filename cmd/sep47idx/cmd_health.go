package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"
)

func init() {
	commands["health"] = command{
		summary: "check HTTP health of a running sep47idx instance",
		run:     runHealth,
	}
}

func runHealth(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	fs.SetOutput(stderr)
	probeURL := fs.String("url", "http://127.0.0.1:8080/v1/health", "health check URL to probe")
	timeout := fs.Duration("timeout", 5*time.Second, "HTTP probe timeout")
	if err := fs.Parse(args); err != nil {
		return readExit("health", err, stderr)
	}
	if fs.NArg() != 0 {
		return readExit("health", fmt.Errorf("takes no positional arguments"), stderr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *probeURL, nil)
	if err != nil {
		errorf(stderr, "sep47idx health: %v\n", err)
		return exitError
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		errorf(stderr, "sep47idx health: %v\n", err)
		return exitError
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		errorf(stderr, "sep47idx health: unexpected HTTP status %d\n", resp.StatusCode)
		return exitError
	}
	_, _ = fmt.Fprintln(stdout, "ok")
	return exitOK
}
