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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var handler http.Handler
	label := cfg.Network.Name
	var cleanup func()
	if config.IsNetworkMap(cfg.DB) {
		if wasSet(fs, "network") {
			return readExit("serve", fmt.Errorf("--network cannot be combined with a multi-network --db"), stderr)
		}
		handler, cleanup, err = multiNetworkHandler(ctx, cfg, *rate)
		label = cfg.DB
	} else {
		handler, cleanup, err = singleNetworkHandler(ctx, cfg, *rate)
	}
	if err != nil {
		return readExit("serve", err, stderr)
	}
	defer cleanup()
	listener, err := net.Listen("tcp", *addr) // #nosec G102 -- operator explicitly chooses the listen address
	if err != nil {
		return readExit("serve", err, stderr)
	}
	defer func() { _ = listener.Close() }()
	if _, err = fmt.Fprintf(stdout, "serving %s read-only on %s\n", label, listener.Addr()); err != nil {
		return readExit("serve", err, stderr)
	}
	return readExit("serve", serveHTTP(ctx, listener, handler), stderr)
}

func singleNetworkHandler(ctx context.Context, cfg config.Common, rate int) (http.Handler, func(), error) {
	if err := cfg.RequireRPC(); err != nil {
		return nil, nil, err
	}
	s, err := store.Open(ctx, cfg.DB, store.Options{Passphrase: cfg.Network.Passphrase, ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = s.Close() }
	client, err := rpc.New(rpc.Config{URL: cfg.RPCURL, Timeout: cfg.RPCTimeout, Concurrency: cfg.Concurrency})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	// Verify the provider's network before using its live tip for health reports.
	network, err := client.GetNetwork(ctx)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if network.Passphrase != cfg.Network.Passphrase {
		cleanup()
		return nil, nil, fmt.Errorf("RPC network does not match %s", cfg.Network.Name)
	}
	h, err := api.New(api.Options{Store: s, Network: cfg.Network.Name, RPC: client, RateLimit: rate})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return h, cleanup, nil
}

// multiNetworkHandler opens one read-only store and RPC client per network.
// The caller keeps it alive until the server returns; stores opened before a
// later error are closed here.
func multiNetworkHandler(ctx context.Context, cfg config.Common, rate int) (http.Handler, func(), error) {
	dbs, err := config.ParseNetworkMap(cfg.DB)
	if err != nil {
		return nil, nil, fmt.Errorf("--db: %w", err)
	}
	if len(dbs) < 2 {
		return nil, nil, fmt.Errorf("multi-network serve needs at least two --db entries")
	}
	if !config.IsNetworkMap(cfg.RPCURL) {
		return nil, nil, fmt.Errorf("multi-network serve needs --rpc-url mainnet=<url>,testnet=<url>")
	}
	rpcURLs, err := config.ParseNetworkMap(cfg.RPCURL)
	if err != nil {
		return nil, nil, fmt.Errorf("--rpc-url: %w", err)
	}
	if len(rpcURLs) != len(dbs) {
		return nil, nil, fmt.Errorf("--db and --rpc-url must name the same networks")
	}
	backends := make(map[string]api.Backend, len(dbs))
	var opened []*store.Store
	cleanup := func() {
		for _, s := range opened {
			_ = s.Close()
		}
	}
	for name, db := range dbs {
		url, ok := rpcURLs[name]
		if !ok {
			cleanup()
			return nil, nil, fmt.Errorf("--rpc-url is missing %s", name)
		}
		n, err := config.NetworkByName(name)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		s, err := store.Open(ctx, db, store.Options{Passphrase: n.Passphrase, ReadOnly: true})
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		opened = append(opened, s)
		client, err := rpc.New(rpc.Config{URL: url, Timeout: cfg.RPCTimeout, Concurrency: cfg.Concurrency})
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		network, err := client.GetNetwork(ctx)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("%s: getNetwork: %w", name, err)
		}
		if network.Passphrase != n.Passphrase {
			cleanup()
			return nil, nil, fmt.Errorf("%s: RPC serves %q", name, network.Passphrase)
		}
		backends[name] = api.Backend{Store: s, Network: name, RPC: client}
	}
	h, err := api.New(api.Options{Networks: backends, RateLimit: rate})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return h, cleanup, nil
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
