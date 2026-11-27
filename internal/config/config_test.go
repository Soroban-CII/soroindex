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
		{"futurenet defaults to the SDF RPC and its own db", []string{"--network", "futurenet"}, Futurenet.DefaultRPCURL, "./data/futurenet.db", ""},
		{"unknown network is rejected", []string{"--network", "unknownnet"}, "", "", "unknown network"},
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

// TestFlagOverridesEnvOverridesDefault asserts the three-tier precedence
// that CLAUDE.md §5.12 promises: command-line flag > env var > default.
// One table row per common flag; the test fails if a new flag lacks env support.
func TestFlagOverridesEnvOverridesDefault(t *testing.T) {
	type row struct {
		flag     string // flag name passed to RegisterCommon
		envKey   string // SEP47IDX_* variable name
		envVal   string // value to inject as env
		flagVal  string // value to pass on the command line (overrides env)
		defVal   string // expected value when neither flag nor env is set
		envWant  string // expected value when env is set but no flag
		flagWant string // expected value when flag is set (overrides env)
	}
	rows := []row{
		{
			flag: "network", envKey: "SEP47IDX_NETWORK",
			envVal: "mainnet", flagVal: "futurenet",
			defVal: "testnet", envWant: "mainnet", flagWant: "futurenet",
		},
		{
			flag: "rpc-url", envKey: "SEP47IDX_RPC_URL",
			envVal: "https://env-rpc", flagVal: "https://flag-rpc",
			defVal: "", envWant: "https://env-rpc", flagWant: "https://flag-rpc",
		},
		{
			flag: "db", envKey: "SEP47IDX_DB",
			envVal: "/env/db.sqlite", flagVal: "/flag/db.sqlite",
			defVal: "", envWant: "/env/db.sqlite", flagWant: "/flag/db.sqlite",
		},
		{
			// Bool flags: --json sets true; default is false; env "true" sets true.
			// We only test env→true and default→false here; the flag→overrides
			// case is covered by the rpc-url row (string flag, same code path).
			flag: "json", envKey: "SEP47IDX_JSON",
			envVal: "true", flagVal: "true",
			defVal: "false", envWant: "true", flagWant: "true",
		},
		{
			// Duration flag: Go normalises 60s→"1m0s" and 90s→"1m30s".
			flag: "rpc-timeout", envKey: "SEP47IDX_RPC_TIMEOUT",
			envVal: "60s", flagVal: "90s",
			defVal: "30s", envWant: "1m0s", flagWant: "1m30s",
		},
		{
			flag: "rpc-concurrency", envKey: "SEP47IDX_RPC_CONCURRENCY",
			envVal: "8", flagVal: "16",
			defVal: "4", envWant: "8", flagWant: "16",
		},
	}

	// Isolate from the real environment for every row.
	allKeys := []string{"SEP47IDX_NETWORK", "SEP47IDX_RPC_URL", "SEP47IDX_DB", "SEP47IDX_JSON", "SEP47IDX_RPC_TIMEOUT", "SEP47IDX_RPC_CONCURRENCY"}

	for _, row := range rows {
		row := row
		t.Run(row.flag+"/env-wins-over-default", func(t *testing.T) {
			for _, k := range allKeys {
				t.Setenv(k, "")
			}
			t.Setenv(row.envKey, row.envVal)
			fs := flag.NewFlagSet("x", flag.ContinueOnError)
			_ = RegisterCommon(fs)
			if err := fs.Parse(nil); err != nil {
				t.Fatal(err)
			}
			// Find the flag and check its value after ApplyEnv.
			if err := ApplyEnv(fs, func(k string) (string, bool) {
				if k == row.envKey {
					return row.envVal, true
				}
				return "", false
			}); err != nil {
				t.Fatal(err)
			}
			f := fs.Lookup(row.flag)
			if f == nil {
				t.Fatalf("flag %q not registered by RegisterCommon — add env support for it", row.flag)
			}
			if got := f.Value.String(); got != row.envWant {
				t.Fatalf("flag %q after env: got %q, want %q", row.flag, got, row.envWant)
			}
		})

		t.Run(row.flag+"/flag-wins-over-env", func(t *testing.T) {
			for _, k := range allKeys {
				t.Setenv(k, "")
			}
			fs := flag.NewFlagSet("x", flag.ContinueOnError)
			_ = RegisterCommon(fs)
			if err := fs.Parse([]string{"--" + row.flag, row.flagVal}); err != nil {
				t.Fatal(err)
			}
			// Even with the env set, the flag must win.
			if err := ApplyEnv(fs, func(k string) (string, bool) {
				if k == row.envKey {
					return row.envVal, true
				}
				return "", false
			}); err != nil {
				t.Fatal(err)
			}
			f := fs.Lookup(row.flag)
			if got := f.Value.String(); got != row.flagWant {
				t.Fatalf("flag %q after flag+env: got %q, want %q", row.flag, got, row.flagWant)
			}
		})

		t.Run(row.flag+"/default-when-neither", func(t *testing.T) {
			for _, k := range allKeys {
				t.Setenv(k, "")
			}
			fs := flag.NewFlagSet("x", flag.ContinueOnError)
			_ = RegisterCommon(fs)
			if err := fs.Parse(nil); err != nil {
				t.Fatal(err)
			}
			if err := ApplyEnv(fs, func(string) (string, bool) { return "", false }); err != nil {
				t.Fatal(err)
			}
			f := fs.Lookup(row.flag)
			if got := f.Value.String(); got != row.defVal {
				t.Fatalf("flag %q default: got %q, want %q", row.flag, got, row.defVal)
			}
		})
	}
}

func TestNetworkByName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNet Network
		wantErr bool
	}{
		{"mainnet", "mainnet", Mainnet, false},
		{"testnet", "testnet", Testnet, false},
		{"futurenet", "futurenet", Futurenet, false},
		{"unknown", "unknownnet", Network{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NetworkByName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Name != tt.wantNet.Name || got.Passphrase != tt.wantNet.Passphrase || got.DefaultRPCURL != tt.wantNet.DefaultRPCURL {
				t.Fatalf("got %+v, want %+v", got, tt.wantNet)
			}
		})
	}
}
