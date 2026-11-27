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
	readHeaderTimeout := fs.Duration("read-header-timeout", 5*time.Second, "maximum time to read request headers")
	readTimeout := fs.Duration("read-timeout", 20*time.Second, "maximum time to read request body")
	writeTimeout := fs.Duration("write-timeout", 20*time.Second, "maximum time to write response")
	idleTimeout := fs.Duration("idle-timeout", 60*time.Second, "maximum time to wait for next request on keep-alive connection")
	maxHeaderBytes := fs.Int("max-header-bytes", 16<<10, "maximum size of request headers in bytes")
	waitForDB := fs.Duration("wait-for-db", 0, "duration to wait for the database to be initialized by sync")
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
	if *readHeaderTimeout < 0 || *readTimeout < 0 || *writeTimeout < 0 || *idleTimeout < 0 {
		return readExit("serve", fmt.Errorf("timeouts must not be negative"), stderr)
	}
	if *maxHeaderBytes < 0 {
		return readExit("serve", fmt.Errorf("max-header-bytes must not be negative"), stderr)
	}
	if *waitForDB < 0 {
		return readExit("serve", fmt.Errorf("wait-for-db must not be negative"), stderr)
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
		handler, cleanup, err = singleNetworkHandler(ctx, cfg, *rate, *waitForDB)
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
	return readExit("serve", serveHTTP(ctx, listener, handler, *readHeaderTimeout, *readTimeout, *writeTimeout, *idleTimeout, *maxHeaderBytes), stderr)
}

func singleNetworkHandler(ctx context.Context, cfg config.Common, rate int, waitForDB time.Duration) (http.Handler, func(), error) {
	if err := cfg.RequireRPC(); err != nil {
		return nil, nil, err
	}
	s, err := openReadOnlyWithWait(ctx, cfg.DB, cfg.Network.Passphrase, waitForDB)
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

func openReadOnlyWithWait(ctx context.Context, dbPath, passphrase string, wait time.Duration) (*store.Store, error) {
	if wait <= 0 {
		return store.Open(ctx, dbPath, store.Options{Passphrase: passphrase, ReadOnly: true})
	}
	deadline := time.Now().Add(wait)
	for {
		var err error
		if _, statErr := os.Stat(dbPath); statErr == nil {
			var s *store.Store
			s, err = store.Open(ctx, dbPath, store.Options{Passphrase: passphrase, ReadOnly: true})
			if err == nil {
				return s, nil
			}
		} else {
			err = statErr
		}
		retryable := errors.Is(err, store.ErrReadOnlyUninitialized) || errors.Is(err, os.ErrNotExist) || errors.Is(err, store.ErrNotFound)
		if !retryable || !time.Now().Before(deadline) {
			return nil, err
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
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

func serveHTTP(ctx context.Context, listener net.Listener, handler http.Handler, readHeaderTimeout, readTimeout, writeTimeout, idleTimeout time.Duration, maxHeaderBytes int) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
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
