package ingest

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// CheckOptions selects the contracts to compare with the network.
type CheckOptions struct {
	Sample int   // number of contracts, 1..100000
	Seed   int64 // changes which contracts are drawn
}

// Difference is one field where the stored index and the network disagree.
type Difference struct {
	ContractID string `json:"contract_id"`
	Field      string `json:"field"`
	Stored     string `json:"stored"`
	RPC        string `json:"rpc"`
	RPCLedger  uint32 `json:"rpc_ledger,omitempty"`
}

// CheckResult summarizes one data-quality run. Entries changed after the
// index's last ledger are skipped, not reported: the index cannot know about
// a change it has not synced yet.
type CheckResult struct {
	LastLedger        uint32       `json:"last_ledger"`
	LatestLedger      uint32       `json:"latest_ledger"`
	Sampled           int          `json:"sampled"`
	Checked           int          `json:"checked"`
	ChangedAfterIndex int          `json:"changed_after_index"`
	Archived          int          `json:"archived"`
	Differences       []Difference `json:"differences"`
}

// Checker compares stored contract state with live RPC entries. It never
// writes: a run is safe against a production database.
type Checker struct {
	Store *store.Store
	RPC   RPC
}

// Check samples contracts, re-reads their instance and reference entries,
// and reports every field that differs. A missing entry is a difference
// unless the index already records the contract as archived.
func (c *Checker) Check(ctx context.Context, o CheckOptions) (CheckResult, error) {
	if o.Sample < 1 || o.Sample > 100000 {
		return CheckResult{}, fmt.Errorf("sample %d: want 1..100000", o.Sample)
	}
	lastStr, err := c.Store.State(ctx, store.KeyLastLedger)
	if err != nil {
		return CheckResult{}, fmt.Errorf("read last ledger: %w", err)
	}
	last64, err := strconv.ParseUint(lastStr, 10, 32)
	if err != nil {
		return CheckResult{}, fmt.Errorf("last ledger %q: %w", lastStr, err)
	}
	last := uint32(last64)
	latest, err := c.RPC.GetLatestLedger(ctx)
	if err != nil {
		return CheckResult{}, fmt.Errorf("get latest ledger: %w", err)
	}
	if latest.Sequence < last {
		return CheckResult{}, fmt.Errorf("RPC latest ledger %d is behind index last ledger %d", latest.Sequence, last)
	}
	sampled, err := c.Store.SampleContracts(ctx, o.Sample, o.Seed)
	if err != nil {
		return CheckResult{}, fmt.Errorf("sample contracts: %w", err)
	}
	res := CheckResult{LastLedger: last, LatestLedger: latest.Sequence, Sampled: len(sampled), Differences: []Difference{}}

	keys := make([]string, 0, len(sampled)*2)
	stateByKey := map[string]store.ContractState{}
	for _, s := range sampled {
		k, err := InstanceKey(s.ID)
		if err != nil {
			return CheckResult{}, fmt.Errorf("instance key %s: %w", s.ID, err)
		}
		keys = append(keys, k)
		stateByKey[k] = s
		if s.Kind == "wasm_ref" && s.ExecRefOwner != "" && s.ExecRefTag != "" {
			rk, err := ExecRefKey(s.ExecRefOwner, s.ExecRefTag)
			if err != nil {
				return CheckResult{}, fmt.Errorf("reference key %s: %w", s.ID, err)
			}
			keys = append(keys, rk)
		}
	}
	entries, err := fetchEntries(ctx, c.RPC, keys)
	if err != nil {
		return CheckResult{}, err
	}

	for _, key := range SortedKeys(stateByKey) {
		s := stateByKey[key]
		e, ok := entries[key]
		if !ok {
			if s.Archived {
				res.Archived++
			} else {
				res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "instance", Stored: s.Kind, RPC: "absent"})
			}
			continue
		}
		if e.LastModifiedLedgerSeq > last {
			res.ChangedAfterIndex++
			continue
		}
		if e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq < latest.Sequence {
			if s.Archived {
				res.Archived++
			} else {
				res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "archived", Stored: "false", RPC: "true", RPCLedger: e.LastModifiedLedgerSeq})
			}
			continue
		}
		if s.Archived {
			res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "archived", Stored: "true", RPC: "false", RPCLedger: e.LastModifiedLedgerSeq})
			continue
		}
		data, err := DecodeLedgerEntryDataBase64(e.XDR, XDRLimits{MaxBase64Len: 1 << 20, MaxDepth: 500})
		if err != nil {
			res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "decode", Stored: s.Kind, RPC: err.Error(), RPCLedger: e.LastModifiedLedgerSeq})
			continue
		}
		if data.Type != xdr.LedgerEntryTypeContractData || data.ContractData == nil {
			res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "kind", Stored: s.Kind, RPC: data.Type.String(), RPCLedger: e.LastModifiedLedgerSeq})
			continue
		}
		inst, err := DecodeInstance(*data.ContractData)
		if err != nil {
			res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "decode", Stored: s.Kind, RPC: err.Error(), RPCLedger: e.LastModifiedLedgerSeq})
			continue
		}
		res.Checked++
		if s.Kind != string(inst.Kind) {
			res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "kind", Stored: s.Kind, RPC: string(inst.Kind), RPCLedger: e.LastModifiedLedgerSeq})
		}
		switch inst.Kind {
		case KindWasm:
			if s.WasmHash != inst.WasmHash {
				res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "wasm_hash", Stored: s.WasmHash, RPC: inst.WasmHash, RPCLedger: e.LastModifiedLedgerSeq})
			}
		case KindSAC:
			if s.SACAsset != inst.SACAsset {
				res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "sac_asset", Stored: s.SACAsset, RPC: inst.SACAsset, RPCLedger: e.LastModifiedLedgerSeq})
			}
		case KindWasmRef:
			if s.ExecRefOwner != inst.RefOwner || s.ExecRefTag != inst.RefTag {
				res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "exec_ref", Stored: refString(s.ExecRefOwner, s.ExecRefTag), RPC: refString(inst.RefOwner, inst.RefTag), RPCLedger: e.LastModifiedLedgerSeq})
			}
			checkReference(&res, entries, s, inst, last, latest.Sequence, e.LastModifiedLedgerSeq)
		}
	}
	sort.Slice(res.Differences, func(i, j int) bool {
		if res.Differences[i].ContractID != res.Differences[j].ContractID {
			return res.Differences[i].ContractID < res.Differences[j].ContractID
		}
		return res.Differences[i].Field < res.Differences[j].Field
	})
	return res, nil
}

// checkReference compares the hash a wasm_ref contract resolves to. A
// reference entry changed after the index's last ledger is skipped, because
// the index has not had a chance to apply that update.
func checkReference(res *CheckResult, entries map[string]rpc.LedgerEntry, s store.ContractState, inst Instance, last, latest, instLedger uint32) {
	if inst.RefOwner == "" || inst.RefTag == "" {
		return
	}
	key, err := ExecRefKey(inst.RefOwner, inst.RefTag)
	if err != nil {
		res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "exec_ref", Stored: s.WasmHash, RPC: err.Error(), RPCLedger: instLedger})
		return
	}
	e, ok := entries[key]
	if !ok {
		res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "wasm_hash", Stored: s.WasmHash, RPC: "unresolved", RPCLedger: instLedger})
		return
	}
	if e.LastModifiedLedgerSeq > last {
		res.ChangedAfterIndex++
		return
	}
	if e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq < latest {
		if !s.Archived {
			res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "archived", Stored: "false", RPC: "true", RPCLedger: e.LastModifiedLedgerSeq})
		}
		return
	}
	data, err := DecodeLedgerEntryDataBase64(e.XDR, XDRLimits{MaxBase64Len: 1 << 20, MaxDepth: 500})
	if err != nil || data.Type != xdr.LedgerEntryTypeContractData || data.ContractData == nil {
		res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "decode", Stored: s.WasmHash, RPC: fmt.Sprint(err), RPCLedger: e.LastModifiedLedgerSeq})
		return
	}
	ref, err := DecodeExecRef(*data.ContractData)
	if err != nil {
		res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "decode", Stored: s.WasmHash, RPC: err.Error(), RPCLedger: e.LastModifiedLedgerSeq})
		return
	}
	if s.WasmHash != ref.WasmHash {
		res.Differences = append(res.Differences, Difference{ContractID: s.ID, Field: "wasm_hash", Stored: s.WasmHash, RPC: ref.WasmHash, RPCLedger: e.LastModifiedLedgerSeq})
	}
}

// fetchEntries reads every key in batches, preserving the node's entries by
// key. A key the node omits is absent from the result.
func fetchEntries(ctx context.Context, c RPC, keys []string) (map[string]rpc.LedgerEntry, error) {
	out := make(map[string]rpc.LedgerEntry, len(keys))
	for start := 0; start < len(keys); start += rpc.MaxLedgerEntryKeys {
		batch := keys[start:min(start+rpc.MaxLedgerEntryKeys, len(keys))]
		res, err := c.GetLedgerEntries(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("get ledger entries at %d: %w", start, err)
		}
		for _, e := range res.Entries {
			out[e.Key] = e
		}
	}
	return out, nil
}

func refString(owner, tag string) string {
	if owner == "" && tag == "" {
		return ""
	}
	return owner + ":" + tag
}
