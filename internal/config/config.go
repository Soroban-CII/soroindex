// Package config resolves command settings from flags and SEP47IDX_*
// environment variables in one place. A flag given on the command line
// always overrides the environment; the environment overrides defaults.
package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// EnvPrefix prefixes every environment variable: --rpc-url is
// SEP47IDX_RPC_URL.
const EnvPrefix = "SEP47IDX_"

// Network identifies a Stellar network by its passphrase, which is what the
// RPC node reports and what the database records.
type Network struct {
	Name       string
	Passphrase string
	// DefaultRPCURL is empty when no public endpoint is assumed; the
	// operator must then pass --rpc-url.
	DefaultRPCURL string
}

// Known networks. SDF runs a public testnet RPC but no public mainnet RPC,
// so mainnet has no default URL.
var (
	Mainnet = Network{Name: "mainnet", Passphrase: "Public Global Stellar Network ; September 2015"}
	Testnet = Network{Name: "testnet", Passphrase: "Test SDF Network ; September 2015", DefaultRPCURL: "https://soroban-testnet.stellar.org"}
)

// ErrUnknownNetwork means --network named no known network.
var ErrUnknownNetwork = errors.New("unknown network")

// NetworkByName returns the network called name ("mainnet" or "testnet").
func NetworkByName(name string) (Network, error) {
	switch name {
	case Mainnet.Name:
		return Mainnet, nil
	case Testnet.Name:
		return Testnet, nil
	}
	return Network{}, fmt.Errorf("%w %q (want mainnet or testnet)", ErrUnknownNetwork, name)
}

// Common holds the flags every command shares (CLAUDE.md §5.12).
type Common struct {
	Network     Network
	RPCURL      string
	DB          string
	JSON        bool
	RPCTimeout  time.Duration
	Concurrency int
}

// commonRaw holds flag values before resolution.
type commonRaw struct {
	network, rpcURL, db string
	json                bool
	rpcTimeout          time.Duration
	concurrency         int
}

// RegisterCommon adds the shared flags to fs and returns a function that
// resolves them after fs.Parse, applying environment variables to any flag
// not set on the command line.
func RegisterCommon(fs *flag.FlagSet) func() (Common, error) {
	var r commonRaw
	fs.StringVar(&r.network, "network", "testnet", "network: mainnet or testnet (env SEP47IDX_NETWORK)")
	fs.StringVar(&r.rpcURL, "rpc-url", "", "Stellar RPC URL (env SEP47IDX_RPC_URL; testnet default "+Testnet.DefaultRPCURL+")")
	fs.StringVar(&r.db, "db", "", "SQLite database path (env SEP47IDX_DB; default ./data/<network>.db)")
	fs.BoolVar(&r.json, "json", false, "print JSON (env SEP47IDX_JSON)")
	fs.DurationVar(&r.rpcTimeout, "rpc-timeout", 30*time.Second, "timeout per RPC attempt (env SEP47IDX_RPC_TIMEOUT)")
	fs.IntVar(&r.concurrency, "rpc-concurrency", 4, "RPC requests in flight (env SEP47IDX_RPC_CONCURRENCY)")
	return func() (Common, error) {
		if err := ApplyEnv(fs, os.LookupEnv); err != nil {
			return Common{}, err
		}
		n, err := NetworkByName(r.network)
		if err != nil {
			return Common{}, err
		}
		c := Common{Network: n, RPCURL: r.rpcURL, DB: r.db, JSON: r.json, RPCTimeout: r.rpcTimeout, Concurrency: r.concurrency}
		if c.RPCURL == "" {
			c.RPCURL = n.DefaultRPCURL
		}
		if c.DB == "" {
			c.DB = "./data/" + n.Name + ".db"
		}
		if c.RPCTimeout <= 0 || c.Concurrency <= 0 {
			return Common{}, errors.New("--rpc-timeout and --rpc-concurrency must be positive")
		}
		return c, nil
	}
}

// RequireRPC returns an error naming the flag when no RPC URL is known.
func (c Common) RequireRPC() error {
	if c.RPCURL == "" {
		return fmt.Errorf("no RPC URL for %s: pass --rpc-url or set %sRPC_URL", c.Network.Name, EnvPrefix)
	}
	return nil
}

// EnvName returns the environment variable for a flag: "rpc-url" ->
// "SEP47IDX_RPC_URL".
func EnvName(flagName string) string {
	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
}

// ApplyEnv sets every flag in fs that was not given on the command line
// from its environment variable, if present and non-empty. An empty
// variable is treated as unset, so "SEP47IDX_RPC_URL=" cannot blank a
// default. lookup is os.LookupEnv in
// production and a map in tests.
func ApplyEnv(fs *flag.FlagSet, lookup func(string) (string, bool)) error {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var err error
	fs.VisitAll(func(f *flag.Flag) {
		if err != nil || set[f.Name] {
			return
		}
		if v, ok := lookup(EnvName(f.Name)); ok && v != "" {
			if e := fs.Set(f.Name, v); e != nil {
				err = fmt.Errorf("%s=%q: %w", EnvName(f.Name), v, e)
			}
		}
	})
	return err
}
