package config

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "sep47idx-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestApplyFile(t *testing.T) {
	t.Run("file sets flags not on command line", func(t *testing.T) {
		path := writeConfig(t, `{"network":"mainnet","rpc-url":"https://file"}`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		net := fs.String("network", "testnet", "")
		url := fs.String("rpc-url", "", "")
		if err := fs.Parse(nil); err != nil {
			t.Fatal(err)
		}
		if err := ApplyFile(fs, path); err != nil {
			t.Fatal(err)
		}
		if *net != "mainnet" || *url != "https://file" {
			t.Fatalf("got network=%q rpc-url=%q", *net, *url)
		}
	})

	t.Run("command-line flag overrides file", func(t *testing.T) {
		path := writeConfig(t, `{"network":"mainnet"}`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		net := fs.String("network", "testnet", "")
		if err := fs.Parse([]string{"--network", "futurenet"}); err != nil {
			t.Fatal(err)
		}
		if err := ApplyFile(fs, path); err != nil {
			t.Fatal(err)
		}
		if *net != "futurenet" {
			t.Fatalf("got network=%q, want futurenet (flag must override file)", *net)
		}
	})

	t.Run("env value overrides file value via precedence order", func(t *testing.T) {
		// ApplyEnv before ApplyFile: env fills unset flags first, then
		// ApplyFile only sets flags still at their default.
		path := writeConfig(t, `{"rpc-url":"https://file"}`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		url := fs.String("rpc-url", "", "")
		if err := fs.Parse(nil); err != nil {
			t.Fatal(err)
		}
		lookup := func(k string) (string, bool) {
			if k == "SEP47IDX_RPC_URL" {
				return "https://env", true
			}
			return "", false
		}
		if err := ApplyEnv(fs, lookup); err != nil {
			t.Fatal(err)
		}
		if err := ApplyFile(fs, path); err != nil {
			t.Fatal(err)
		}
		if *url != "https://env" {
			t.Fatalf("got rpc-url=%q, want https://env (env must override file)", *url)
		}
	})

	t.Run("unknown key is an error naming the key", func(t *testing.T) {
		path := writeConfig(t, `{"unknown-flag":"value"}`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.String("network", "testnet", "")
		_ = fs.Parse(nil)
		err := ApplyFile(fs, path)
		if err == nil || !strings.Contains(err.Error(), "unknown-flag") {
			t.Fatalf("err = %v, want it to mention the key name", err)
		}
	})

	t.Run("empty path is a no-op", func(t *testing.T) {
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		net := fs.String("network", "testnet", "")
		_ = fs.Parse(nil)
		if err := ApplyFile(fs, ""); err != nil {
			t.Fatalf("empty path: %v", err)
		}
		if *net != "testnet" {
			t.Fatal("empty path must not change defaults")
		}
	})

	t.Run("missing file is an error", func(t *testing.T) {
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.String("network", "testnet", "")
		_ = fs.Parse(nil)
		if err := ApplyFile(fs, "/no/such/file.json"); err == nil {
			t.Fatal("want error for missing file")
		}
	})

	t.Run("invalid JSON is an error", func(t *testing.T) {
		path := writeConfig(t, `not json`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.String("network", "testnet", "")
		_ = fs.Parse(nil)
		if err := ApplyFile(fs, path); err == nil {
			t.Fatal("want error for invalid JSON")
		}
	})

	t.Run("boolean value from file", func(t *testing.T) {
		path := writeConfig(t, `{"json":true}`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		js := fs.Bool("json", false, "")
		_ = fs.Parse(nil)
		if err := ApplyFile(fs, path); err != nil {
			t.Fatal(err)
		}
		if !*js {
			t.Fatal("json flag should be true from file")
		}
	})

	t.Run("numeric value from file", func(t *testing.T) {
		path := writeConfig(t, `{"rpc-concurrency":8}`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		c := fs.Int("rpc-concurrency", 4, "")
		_ = fs.Parse(nil)
		if err := ApplyFile(fs, path); err != nil {
			t.Fatal(err)
		}
		if *c != 8 {
			t.Fatalf("rpc-concurrency = %d, want 8", *c)
		}
	})

	t.Run("duration string value from file", func(t *testing.T) {
		path := writeConfig(t, `{"rpc-timeout":"60s"}`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		resolve := RegisterCommon(fs)
		if err := fs.Parse([]string{"--config", path, "--network", "testnet"}); err != nil {
			t.Fatal(err)
		}
		// Blank env so the file value survives ApplyEnv.
		for _, k := range []string{"NETWORK", "RPC_URL", "DB", "JSON", "RPC_TIMEOUT", "RPC_CONCURRENCY"} {
			t.Setenv(EnvPrefix+k, "")
		}
		c, err := resolve()
		if err != nil {
			t.Fatal(err)
		}
		if c.RPCTimeout.Seconds() != 60 {
			t.Fatalf("rpc-timeout = %v, want 60s", c.RPCTimeout)
		}
	})

	t.Run("config key in file is silently ignored (no self-referential error)", func(t *testing.T) {
		inner := writeConfig(t, `{"network":"mainnet"}`)
		path := writeConfig(t, `{"network":"futurenet","config":"`+inner+`"}`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.String("config", "", "")
		net := fs.String("network", "testnet", "")
		_ = fs.Parse(nil)
		if err := ApplyFile(fs, path); err != nil {
			t.Fatalf("config key in file should not error: %v", err)
		}
		if *net != "futurenet" {
			t.Fatalf("network = %q, want futurenet", *net)
		}
	})

	t.Run("secret flags are not set from file", func(t *testing.T) {
		// Register a fake secret flag for the duration of this test, then
		// restore the original secretFlags map afterwards.
		orig := secretFlags
		secretFlags = map[string]bool{"rpc-url": true}
		defer func() { secretFlags = orig }()

		path := writeConfig(t, `{"rpc-url":"https://file-secret"}`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		url := fs.String("rpc-url", "https://default", "")
		_ = fs.Parse(nil)
		if err := ApplyFile(fs, path); err != nil {
			t.Fatal(err)
		}
		if *url != "https://default" {
			t.Fatalf("secret flag should not be set from file, got %q", *url)
		}
	})
}

func TestRegisterCommonConfigFile(t *testing.T) {
	for _, k := range []string{"NETWORK", "RPC_URL", "DB", "JSON", "RPC_TIMEOUT", "RPC_CONCURRENCY", "CONFIG"} {
		t.Setenv(EnvPrefix+k, "")
	}

	t.Run("--config flag loads file for common flags", func(t *testing.T) {
		path := writeConfig(t, `{"network":"mainnet","rpc-url":"https://cfg"}`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		resolve := RegisterCommon(fs)
		if err := fs.Parse([]string{"--config", path}); err != nil {
			t.Fatal(err)
		}
		c, err := resolve()
		if err != nil {
			t.Fatal(err)
		}
		if c.Network.Name != "mainnet" || c.RPCURL != "https://cfg" {
			t.Fatalf("got network=%q rpc-url=%q", c.Network.Name, c.RPCURL)
		}
	})

	t.Run("SEP47IDX_CONFIG env var loads config file", func(t *testing.T) {
		path := writeConfig(t, `{"network":"mainnet"}`)
		t.Setenv(EnvPrefix+"CONFIG", path)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		resolve := RegisterCommon(fs)
		if err := fs.Parse(nil); err != nil {
			t.Fatal(err)
		}
		c, err := resolve()
		if err != nil {
			t.Fatal(err)
		}
		if c.Network.Name != "mainnet" {
			t.Fatalf("got network=%q, want mainnet from SEP47IDX_CONFIG", c.Network.Name)
		}
	})

	t.Run("command-line flag overrides config file value", func(t *testing.T) {
		path := writeConfig(t, `{"network":"mainnet"}`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		resolve := RegisterCommon(fs)
		if err := fs.Parse([]string{"--config", path, "--network", "testnet"}); err != nil {
			t.Fatal(err)
		}
		c, err := resolve()
		if err != nil {
			t.Fatal(err)
		}
		if c.Network.Name != "testnet" {
			t.Fatalf("got network=%q, want testnet (CLI overrides file)", c.Network.Name)
		}
	})

	t.Run("env var overrides config file value", func(t *testing.T) {
		path := writeConfig(t, `{"network":"mainnet"}`)
		t.Setenv(EnvPrefix+"NETWORK", "futurenet")
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		resolve := RegisterCommon(fs)
		if err := fs.Parse([]string{"--config", path}); err != nil {
			t.Fatal(err)
		}
		c, err := resolve()
		if err != nil {
			t.Fatal(err)
		}
		if c.Network.Name != "futurenet" {
			t.Fatalf("got network=%q, want futurenet (env overrides file)", c.Network.Name)
		}
	})

	t.Run("config file with unknown key is rejected", func(t *testing.T) {
		path := writeConfig(t, `{"does-not-exist":"value"}`)
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		resolve := RegisterCommon(fs)
		if err := fs.Parse([]string{"--config", path}); err != nil {
			t.Fatal(err)
		}
		_, err := resolve()
		if err == nil || !strings.Contains(err.Error(), "does-not-exist") {
			t.Fatalf("err = %v, want it to name the unknown key", err)
		}
	})

	t.Run("every common flag can be loaded from a config file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "cfg.json")
		if err := os.WriteFile(path, []byte(`{
			"network":"mainnet",
			"rpc-url":"https://cfg-rpc",
			"db":"/tmp/cfg.db",
			"json":true,
			"rpc-timeout":"45s",
			"rpc-concurrency":2
		}`), 0o600); err != nil {
			t.Fatal(err)
		}
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		resolve := RegisterCommon(fs)
		if err := fs.Parse([]string{"--config", path}); err != nil {
			t.Fatal(err)
		}
		c, err := resolve()
		if err != nil {
			t.Fatal(err)
		}
		if c.Network.Name != "mainnet" {
			t.Errorf("network = %q", c.Network.Name)
		}
		if c.RPCURL != "https://cfg-rpc" {
			t.Errorf("rpc-url = %q", c.RPCURL)
		}
		if c.DB != "/tmp/cfg.db" {
			t.Errorf("db = %q", c.DB)
		}
		if !c.JSON {
			t.Errorf("json = false, want true")
		}
		if c.RPCTimeout.Seconds() != 45 {
			t.Errorf("rpc-timeout = %v", c.RPCTimeout)
		}
		if c.Concurrency != 2 {
			t.Errorf("rpc-concurrency = %d", c.Concurrency)
		}
	})
}
