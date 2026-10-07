package phase0

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// SampleConfig controls the testnet sample.
type SampleConfig struct {
	Target   int    // contracts wanted; default 2000
	Strata   int    // equal ledger windows across retention; default 20
	PageSize uint32 // ledgers per getLedgers call; default 10 (testnet meta is large)
	// MaxLedgersPerStratum stops a quiet stratum from scanning forever.
	// Default: the whole stratum.
	MaxLedgersPerStratum uint32
}

func (c SampleConfig) withDefaults() SampleConfig {
	if c.Target <= 0 {
		c.Target = 2000
	}
	if c.Strata <= 0 {
		c.Strata = 20
	}
	if c.PageSize == 0 || c.PageSize > rpc.MaxLedgersLimit {
		c.PageSize = 10
	}
	return c
}

// SampleStats records how the sample was drawn, for the report.
type SampleStats struct {
	OldestLedger, LatestLedger uint32
	Strata                     int
	LedgersScanned             int
	PerStratum                 []int // contracts first seen in each stratum
	SkippedEntries             int   // instance entries that failed to decode
}

// metaLimits bounds one ledger's metadata. Testnet ledgers measured on
// 2026-10-07 were about 200 KB of base64; 64 MiB leaves ample room.
var metaLimits = ingest.XDRLimits{MaxBase64Len: 64 << 20}

// SampleTestnet draws contracts from the RPC retention window. The window is
// split into equal strata; each stratum is scanned from its start until it
// has contributed its share (Target/Strata, rounded up) of contracts not
// seen before, or it ends. A contract is any contract whose instance entry
// was created or updated in a scanned ledger.
//
// This samples by activity: a contract that changes its instance often is
// more likely to be drawn than an idle one. The report says so.
func SampleTestnet(ctx context.Context, c ingest.RPC, cfg SampleConfig, log *slog.Logger) ([]string, SampleStats, error) {
	cfg = cfg.withDefaults()
	latest, err := c.GetLatestLedger(ctx)
	if err != nil {
		return nil, SampleStats{}, err
	}
	// A successful call reports the retention window; error text is never parsed.
	probe, err := c.GetLedgers(ctx, rpc.LedgersRequest{StartLedger: latest.Sequence, Limit: 1})
	if err != nil {
		return nil, SampleStats{}, fmt.Errorf("probe retention window: %w", err)
	}
	st := SampleStats{OldestLedger: probe.OldestLedger, LatestLedger: latest.Sequence, Strata: cfg.Strata}
	if st.OldestLedger == 0 || st.OldestLedger > st.LatestLedger {
		return nil, st, fmt.Errorf("RPC reported retention window %d..%d", st.OldestLedger, st.LatestLedger)
	}
	span := st.LatestLedger - st.OldestLedger + 1
	quota := (cfg.Target + cfg.Strata - 1) / cfg.Strata
	seen := map[string]bool{}
	var ids []string
	for s := 0; s < cfg.Strata && len(ids) < cfg.Target; s++ {
		from := st.OldestLedger + stratumOffset(span, s, cfg.Strata)
		to := st.OldestLedger + stratumOffset(span, s+1, cfg.Strata) - 1
		if cfg.MaxLedgersPerStratum > 0 && to-from+1 > cfg.MaxLedgersPerStratum {
			to = from + cfg.MaxLedgersPerStratum - 1
		}
		got := 0
		for next := from; next <= to && got < quota && len(ids) < cfg.Target; {
			limit := min(cfg.PageSize, to-next+1)
			page, err := c.GetLedgers(ctx, rpc.LedgersRequest{StartLedger: next, Limit: limit})
			if err != nil {
				return nil, st, fmt.Errorf("stratum %d ledgers %d+%d: %w", s, next, limit, err)
			}
			before := next
			for _, l := range page.Ledgers {
				if l.Sequence > to {
					break
				}
				st.LedgersScanned++
				var m xdr.LedgerCloseMeta
				if err := ingest.UnmarshalBase64(l.MetadataXDR, &m, metaLimits); err != nil {
					return nil, st, fmt.Errorf("ledger %d meta: %w", l.Sequence, err)
				}
				f, err := ingest.ExtractContractFacts(m)
				if err != nil {
					return nil, st, fmt.Errorf("ledger %d: %w", l.Sequence, err)
				}
				st.SkippedEntries += len(f.Skipped)
				for _, in := range f.Instances {
					if seen[in.ContractID] || got >= quota || len(ids) >= cfg.Target {
						continue
					}
					seen[in.ContractID] = true
					ids = append(ids, in.ContractID)
					got++
				}
				next = l.Sequence + 1
			}
			if next == before {
				break // the page held nothing in this stratum: stop rather than loop
			}
		}
		st.PerStratum = append(st.PerStratum, got)
		log.LogAttrs(ctx, slog.LevelInfo, "phase0 stratum", slog.Int("stratum", s), slog.Int("contracts", got),
			slog.Uint64("from", uint64(from)), slog.Uint64("to", uint64(to)))
	}
	return ids, st, nil
}

// stratumOffset returns floor(span*i/n): where stratum i of n starts,
// relative to the oldest ledger. For 0 <= i <= n it lies in [0, span], so it
// always fits a uint32.
func stratumOffset(span uint32, i, n int) uint32 {
	if n <= 0 || i <= 0 {
		return 0
	}
	if i >= n {
		return span
	}
	off := int64(span) * int64(i) / int64(n)
	return uint32(off) // #nosec G115 -- 0 <= off <= span, checked above
}

// CurrentInstances fetches each contract's current instance entry. A
// contract the node does not return, or whose instance TTL has passed, is
// marked Archived.
func CurrentInstances(ctx context.Context, c ingest.RPC, ids []string) ([]Contract, uint32, error) {
	keyToID := map[string]string{}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		k, err := ingest.InstanceKey(id)
		if err != nil {
			return nil, 0, err
		}
		keyToID[k] = id
		keys = append(keys, k)
	}
	found := map[string]Contract{}
	var latest uint32
	for start := 0; start < len(keys); start += rpc.MaxLedgerEntryKeys {
		res, err := c.GetLedgerEntries(ctx, keys[start:min(start+rpc.MaxLedgerEntryKeys, len(keys))])
		if err != nil {
			return nil, 0, fmt.Errorf("fetch instances: %w", err)
		}
		latest = max(latest, res.LatestLedger)
		for _, e := range res.Entries {
			id := keyToID[e.Key]
			if id == "" {
				return nil, 0, fmt.Errorf("node returned unrequested key %s", e.Key)
			}
			if e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq < res.LatestLedger {
				continue
			}
			data, err := ingest.DecodeLedgerEntryDataBase64(e.XDR, instanceLimits)
			if err != nil || data.Type != xdr.LedgerEntryTypeContractData {
				return nil, 0, fmt.Errorf("instance %s: undecodable (%v)", id, err)
			}
			inst, err := ingest.DecodeInstance(*data.ContractData)
			if err != nil {
				return nil, 0, fmt.Errorf("instance %s: %w", id, err)
			}
			found[id] = fromInstance(inst)
		}
	}
	out := make([]Contract, 0, len(ids))
	for _, id := range ids {
		if ct, ok := found[id]; ok {
			out = append(out, ct)
		} else {
			out = append(out, Contract{ID: id, Archived: true})
		}
	}
	return out, latest, nil
}
