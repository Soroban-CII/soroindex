package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Soroban-CII/soroindex/internal/api"
	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
)

func init() {
	commands["serve"] = command{summary: "serve the index through the read-only JSON API", run: runServe}
}

func runServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := config.RegisterCommon(fs)
	addr := fs.String("addr", ":8080", "HTTP listen address")
	rate := fs.Int("rate-limit", 0, "requests per minute per peer IP; 0 disables limiting")
	if err := fs.Parse(args); err != nil {
		return readExit("serve", err, stderr)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("serve", err, stderr)
	}
	if fs.NArg() != 0 || *rate < 0 {
		return readExit("serve", fmt.Errorf("invalid rate-limit or positional arguments"), stderr)
	}
	if err = cfg.RequireRPC(); err != nil {
		return readExit("serve", err, stderr)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := store.Open(ctx, cfg.DB, store.Options{Passphrase: cfg.Network.Passphrase, ReadOnly: true})
	if err != nil {
		return readExit("serve", err, stderr)
	}
	defer func() { _ = s.Close() }()
	client, err := rpc.New(rpc.Config{URL: cfg.RPCURL, Timeout: cfg.RPCTimeout, Concurrency: cfg.Concurrency})
	if err != nil {
		return readExit("serve", err, stderr)
	}
	// Verify the provider's network before using its live tip for health reports.
	network, err := client.GetNetwork(ctx)
	if err != nil {
		return readExit("serve", err, stderr)
	}
	if network.Passphrase != cfg.Network.Passphrase {
		return readExit("serve", fmt.Errorf("RPC network does not match %s", cfg.Network.Name), stderr)
	}
	handler, err := api.New(api.Options{Store: s, Network: cfg.Network.Name, RPC: client, RateLimit: *rate})
	if err != nil {
		return readExit("serve", err, stderr)
	}
	listener, err := net.Listen("tcp", *addr) // #nosec G102 -- operator explicitly chooses the listen address
	if err != nil {
		return readExit("serve", err, stderr)
	}
	defer func() { _ = listener.Close() }()
	if _, err = fmt.Fprintf(stdout, "serving %s read-only on %s\n", cfg.Network.Name, listener.Addr()); err != nil {
		return readExit("serve", err, stderr)
	}
	return readExit("serve", serveHTTP(ctx, listener, handler), stderr)
}

func serveHTTP(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
