package config

import (
	"flag"
	"io"
	"strings"
	"testing"
)

func TestApplyEnv(t *testing.T) {
	env := map[string]string{"SEP47IDX_RPC_URL": "https://env", "SEP47IDX_NETWORK": "mainnet", "SEP47IDX_JSON": "true"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	t.Run("env fills flags not given on the command line", func(t *testing.T) {
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		url := fs.String("rpc-url", "", "")
		net := fs.String("network", "testnet", "")
		js := fs.Bool("json", false, "")
		if err := fs.Parse(nil); err != nil {
			t.Fatal(err)
		}
		if err := ApplyEnv(fs, lookup); err != nil {
			t.Fatal(err)
		}
		if *url != "https://env" || *net != "mainnet" || !*js {
			t.Fatalf("got %q %q %v", *url, *net, *js)
		}
	})
	t.Run("a flag on the command line overrides env", func(t *testing.T) {
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		url := fs.String("rpc-url", "", "")
		if err := fs.Parse([]string{"--rpc-url", "https://flag"}); err != nil {
			t.Fatal(err)
		}
		if err := ApplyEnv(fs, lookup); err != nil {
			t.Fatal(err)
		}
		if *url != "https://flag" {
			t.Fatalf("got %q", *url)
		}
	})
	t.Run("empty env value is treated as unset", func(t *testing.T) {
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		net := fs.String("network", "testnet", "")
		_ = fs.Parse(nil)
		if err := ApplyEnv(fs, func(string) (string, bool) { return "", true }); err != nil || *net != "testnet" {
			t.Fatalf("network = %q, err %v", *net, err)
		}
	})
	t.Run("invalid env value is an error naming the variable", func(t *testing.T) {
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.Int("rpc-concurrency", 4, "")
		_ = fs.Parse(nil)
		err := ApplyEnv(fs, func(string) (string, bool) { return "lots", true })
		if err == nil || !strings.Contains(err.Error(), "SEP47IDX_RPC_CONCURRENCY") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRegisterCommonDefaults(t *testing.T) {
	// Empty values count as unset, so these isolate the test from the
	// caller's environment.
	for _, k := range []string{"NETWORK", "RPC_URL", "DB", "JSON", "RPC_TIMEOUT", "RPC_CONCURRENCY"} {
		t.Setenv(EnvPrefix+k, "")
	}
	tests := []struct {
		name    string
		args    []string
		wantURL string
		wantDB  string
		wantErr string
	}{
		{"testnet defaults to the SDF RPC and its own db", []string{"--network", "testnet"}, Testnet.DefaultRPCURL, "./data/testnet.db", ""},
		{"mainnet has no default RPC", []string{"--network", "mainnet"}, "", "./data/mainnet.db", ""},
		{"unknown network is rejected", []string{"--network", "futurenet"}, "", "", "unknown network"},
		{"non-positive concurrency is rejected", []string{"--rpc-concurrency", "0"}, "", "", "must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("x", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			resolve := RegisterCommon(fs)
			if err := fs.Parse(tt.args); err != nil {
				t.Fatal(err)
			}
			c, err := resolve()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.RPCURL != tt.wantURL || c.DB != tt.wantDB {
				t.Fatalf("got url %q db %q", c.RPCURL, c.DB)
			}
			if (c.RequireRPC() != nil) != (tt.wantURL == "") {
				t.Fatalf("RequireRPC = %v", c.RequireRPC())
			}
		})
	}
}
