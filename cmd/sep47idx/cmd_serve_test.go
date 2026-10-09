package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/api"
	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
)

func TestServeHTTPReadsIndexAndShutsDown(t *testing.T) {
	db, _, _ := readFixture(t)
	s, err := store.Open(context.Background(), db, store.Options{Passphrase: config.Testnet.Passphrase, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	handler, err := api.New(api.Options{Store: s, Network: "testnet"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, listener, handler) }()
	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/v1/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var d store.Statistics
	if err = json.NewDecoder(response.Body).Decode(&d); err != nil || response.StatusCode != 200 || d.TotalContracts != 1 {
		t.Fatal(d, err)
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not complete")
	}
}

func TestServeCLIRejectsInvalidConfiguration(t *testing.T) {
	for _, args := range [][]string{{"serve", "--rate-limit", "-1"}, {"serve", "extra"}, {"serve", "--db", "/missing/index.db"}, {"serve", "--network", "mainnet"}} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
			t.Fatal(args, code)
		}
	}
}
