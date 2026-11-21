package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Soroban-CII/soroindex/internal/claims"
	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	indexsync "github.com/Soroban-CII/soroindex/internal/sync"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
)

func init() {
	commands["sync"] = command{
		summary: "seed the index, sync ledgers or recompute matches",
		run:     runSync,
	}
}

// runSync implements the seed and recompute paths of CLAUDE.md §5.9:
//
//	sep47idx sync --network <n> --seed <file>
//	sep47idx sync --network <n> --recompute
func runSync(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sync", flag.ContinueOnError)
	flags.SetOutput(stderr)
	resolve := config.RegisterCommon(flags)
	seed := flags.String("seed", "", "CSV of contract_id[,wasm_hash] to seed from (e.g. the Hubble Q2 export)")
	allowInvocation := flags.Bool("allow-invocation", false, "run claim sources that simulate contract calls (none ship by default)")
	recompute := flags.Bool("recompute", false, "re-run the matcher for Wasm whose stored ruleset_version differs from the loaded rules (no network)")
	threshold := flags.Float64("partial-threshold", match.DefaultPartialThreshold, "match.partial_threshold")
	start := flags.Uint64("start-ledger", 0, "first ledger to index on an unseeded database")
	stop := flags.Uint64("stop-ledger", 0, "stop after committing this ledger; 0 means no limit (must be >= start-ledger when both are set)")
	follow := flags.Bool("follow", false, "poll for new ledgers after catching up")
	interval := flags.Duration("interval", 5*time.Second, "poll interval when following")
	verifyURL := flags.String("verify-url", "", "secondary RPC URL to cross-check ledger hashes against the primary (must serve the same network)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitError
	}
	cfg, err := resolve()
	if err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	if *start > uint64(^uint32(0)) || (*start == 0 && wasSet(flags, "start-ledger")) || *interval <= 0 || flags.NArg() != 0 {
		errorf(stderr, "sep47idx sync: start-ledger must be 1..4294967295, interval must be positive, and positional arguments are not accepted\n")
		return exitError
	}
	if *stop > uint64(^uint32(0)) || (*stop == 0 && wasSet(flags, "stop-ledger")) {
		errorf(stderr, "sep47idx sync: stop-ledger must be 1..4294967295\n")
		return exitError
	}
	if *stop > 0 && *start > 0 && *stop < *start {
		errorf(stderr, "sep47idx sync: --stop-ledger %d must not be less than --start-ledger %d\n", *stop, *start)
		return exitError
	}
	incremental := *seed == "" && !*recompute || *start != 0 || *follow
	log := slog.New(slog.NewJSONHandler(stderr, nil))
	ctx, cancelSig := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancelSig()

	// Recompute alone needs no network; seeding does.
	var client *rpc.Client
	if *seed != "" || incremental {
		if err := cfg.RequireRPC(); err != nil {
			errorf(stderr, "sep47idx sync: %v\n", err)
			return exitError
		}
		c, err := rpc.New(rpc.Config{URL: cfg.RPCURL, Timeout: cfg.RPCTimeout, Concurrency: cfg.Concurrency, Logger: log})
		if err != nil {
			errorf(stderr, "sep47idx sync: %v\n", err)
			return exitError
		}
		n, err := c.GetNetwork(ctx)
		if err != nil {
			errorf(stderr, "sep47idx sync: getNetwork: %v\n", err)
			return exitError
		}
		if n.Passphrase != cfg.Network.Passphrase {
			errorf(stderr, "sep47idx sync: RPC %s serves %q, not %s\n", cfg.RPCURL, n.Passphrase, cfg.Network.Name)
			return exitError
		}
		client = c
	}
	ruleFiles, err := match.LoadRules(rules.FS)
	if err != nil {
		errorf(stderr, "sep47idx sync: rules: %v\n", err)
		return exitError
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DB), 0o750); err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	st, err := store.Open(ctx, cfg.DB, store.Options{Passphrase: cfg.Network.Passphrase})
	if err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	defer func() { _ = st.Close() }() // close error after a completed seed carries nothing actionable

	reg := claims.Default(sepmeta.DefaultLimits())
	reg.AllowInvocation = *allowInvocation
	ix := &ingest.Indexer{Store: st, Rules: ruleFiles, Matcher: match.Matcher{PartialThreshold: *threshold},
		Limits: sepmeta.DefaultLimits(), Claims: reg, Log: log, Now: time.Now}
	if client != nil {
		ix.RPC = client
		if *seed != "" {
			if err := st.SetState(ctx, store.KeyRPCURL, cfg.RPCURL); err != nil {
				errorf(stderr, "sep47idx sync: %v\n", err)
				return exitError
			}
			if code := runSeed(ctx, ix, *seed, stdout, stderr); code != exitOK {
				return code
			}
		}
	}
	if *recompute {
		rs, err := ix.Recompute(ctx)
		if err != nil {
			errorf(stderr, "sep47idx sync: recompute: %v\n", err)
			return exitError
		}
		if _, err := fmt.Fprintf(stdout, "recompute: %d recomputed, %d no_spec, %d need a re-fetch (analyzed before migration 0002), %d archived before parsing\n",
			rs.Recomputed, rs.NoSpec, rs.NeedsRefetch, rs.NeverParsed); err != nil {
			return exitError
		}
	}
	if incremental {
		loop := indexsync.Loop{Store: st, Source: &ingest.LedgerSource{RPC: client}, Apply: ix.ApplyLedger,
			StartLedger: uint32(*start), StopLedger: uint32(*stop), Follow: *follow, Interval: *interval} // #nosec G115 -- checked above
		loop.Apply = func(ctx context.Context, tx *store.Tx, facts ingest.ContractFacts) error {
			if err := tx.SetState(ctx, store.KeyRPCURL, cfg.RPCURL); err != nil {
				return err
			}
			if _, err := tx.State(ctx, store.KeyRulesetVersions); errors.Is(err, store.ErrNotFound) {
				var versions []string
				for _, rule := range ix.Rules {
					versions = append(versions, rule.RulesetVersion)
				}
				if err := tx.SetState(ctx, store.KeyRulesetVersions, strings.Join(versions, ",")); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			return ix.ApplyLedger(ctx, tx, facts)
		}
		loop.Progress = func(last, latest uint32, caught bool) {
			log.InfoContext(ctx, "sync progress", "last_ledger", last, "latest_ledger", latest, "lag", latest-last, "caught_up", caught)
		}
		if *verifyURL != "" {
			vc, verr := rpc.New(rpc.Config{URL: *verifyURL, Timeout: cfg.RPCTimeout, Concurrency: cfg.Concurrency, Logger: log})
			if verr != nil {
				errorf(stderr, "sep47idx sync: verify-url: %v\n", verr)
				return exitError
			}
			vn, verr := vc.GetNetwork(ctx)
			if verr != nil {
				errorf(stderr, "sep47idx sync: verify-url getNetwork: %v\n", verr)
				return exitError
			}
			if vn.Passphrase != cfg.Network.Passphrase {
				errorf(stderr, "sep47idx sync: verify-url serves %q, not %s\n", vn.Passphrase, cfg.Network.Name)
				return exitError
			}
			vs := &ingest.LedgerSource{RPC: vc}
			loop.VerifyBatch = func(vctx context.Context, b ingest.Batch) error {
				return verifyHashes(vctx, vs, b)
			}
		}
		if err := loop.Run(ctx); err != nil {
			errorf(stderr, "sep47idx sync: %v\n", err)
			return exitError
		}
	}
	return exitOK
}

func wasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func runSeed(ctx context.Context, ix *ingest.Indexer, seed string, stdout, stderr io.Writer) int {
	if err := recordRulesetsIfUnset(ctx, ix.Store, ix.Rules); err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	f, err := os.Open(seed) // #nosec G304 -- operator-supplied seed path
	if err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	defer func() { _ = f.Close() }() // read-only
	res, err := ix.Seed(ctx, ingest.SeedFileSource{R: f})
	if err != nil {
		errorf(stderr, "sep47idx sync: %v\n", err)
		return exitError
	}
	if _, err := fmt.Fprintf(stdout, "seeded %d of %d contracts (%d not found, %d archived, %d hash hints differed); fetched %d wasm; last_ledger %d\n",
		res.Seeded, res.Requested, res.NotFound, res.Archived, res.HintMismatch, res.WasmFetched, res.LastLedger); err != nil {
		return exitError
	}
	return exitOK
}

// verifyHashes fetches the same ledger range from a secondary source and
// returns an error if any hash differs from the primary batch. Write nothing
// is guaranteed by the caller: VerifyBatch runs before commit.
func verifyHashes(ctx context.Context, s *ingest.LedgerSource, b ingest.Batch) error {
	if len(b.Hashes) == 0 {
		return nil
	}
	sb, err := s.Fetch(ctx, b.From)
	if err != nil {
		return fmt.Errorf("verify-url fetch from %d: %w", b.From, err)
	}
	for i, h := range b.Hashes {
		if i >= len(sb.Hashes) {
			return fmt.Errorf("verify-url returned only %d ledgers, primary returned %d", len(sb.Hashes), len(b.Hashes))
		}
		if h != sb.Hashes[i] {
			return fmt.Errorf("ledger %d hash mismatch: primary %s, secondary %s",
				b.From+uint32(i), h, sb.Hashes[i]) // #nosec G115 -- i < 200
		}
	}
	return nil
}

// recordRulesetsIfUnset records the loaded ruleset versions in a fresh
// database. Once set, only --recompute changes them, because only it brings
// every stored match up to the loaded rules.
func recordRulesetsIfUnset(ctx context.Context, st *store.Store, rf []match.RuleFile) error {
	if _, err := st.State(ctx, store.KeyRulesetVersions); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	var versions []string
	for _, r := range rf {
		versions = append(versions, r.RulesetVersion)
	}
	return st.SetState(ctx, store.KeyRulesetVersions, strings.Join(versions, ","))
}
