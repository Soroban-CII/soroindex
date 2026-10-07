// Package phase0 measures SEP-47 adoption across a network: the adoption
// report that gates the rest of the project (CLAUDE.md §5.10). It parses
// Wasm with pkg/sepmeta and checks interfaces with internal/match, through
// the same ingest.Analyze the indexer uses; it never executes Wasm.
package phase0

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Contract is one contract in the census, resolved to the Wasm it runs now.
type Contract struct {
	ID       string
	Kind     ingest.ExecKind
	WasmHash string // resolved hash; "" for SAC or an unresolved reference
	RefOwner string // CAP-85 reference, if Kind is wasm_ref
	RefTag   string
	SACAsset string
	// Unresolved is true for a wasm_ref whose reference entry is missing or
	// archived. Such a contract claims nothing (CLAUDE.md §5.9).
	Unresolved bool
	// Archived is true when the instance itself is missing or archived
	// (testnet sample only; Hubble's current snapshot excludes deleted rows).
	Archived bool
}

// CodeHash is one row of the Hubble contract-code export.
type CodeHash struct {
	Hash    string
	Deleted bool
}

// Census is the population phase0 measures.
type Census struct {
	Network   string
	Method    string // how the population was obtained, for the report
	Contracts []Contract
	// CodeHashes is every code hash known for the network (mainnet: Hubble
	// Q1). Empty for the testnet sample, whose hash population is the
	// hashes its sampled contracts run.
	CodeHashes []CodeHash
	// InstanceDecodeErrors counts instance rows that could not be decoded;
	// they are reported, not silently dropped.
	InstanceDecodeErrors int
	// LatestLedger is the RPC's latest ledger when the census was taken.
	LatestLedger uint32
}

// csvReader returns a reader that maps header names to column indexes and
// rejects files missing any required column.
func csvReader(r io.Reader, required ...string) (*csv.Reader, map[string]int, error) {
	cr := csv.NewReader(r)
	cr.ReuseRecord = false
	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read header: %w", err)
	}
	cols := map[string]int{}
	for i, h := range header {
		cols[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}
	for _, name := range required {
		if _, ok := cols[name]; !ok {
			return nil, nil, fmt.Errorf("missing column %q (have %v)", name, header)
		}
	}
	return cr, cols, nil
}

// ReadCodeHashes reads Hubble Q1 (contract_code_hash, deleted, ...).
func ReadCodeHashes(r io.Reader) ([]CodeHash, error) {
	cr, cols, err := csvReader(r, "contract_code_hash", "deleted")
	if err != nil {
		return nil, fmt.Errorf("hashes csv: %w", err)
	}
	var out []CodeHash
	seen := map[string]bool{}
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("hashes csv line %d: %w", line, err)
		}
		h := strings.ToLower(strings.TrimSpace(rec[cols["contract_code_hash"]]))
		if _, err := ingest.ParseWasmHash(h); err != nil {
			return nil, fmt.Errorf("hashes csv line %d: %w", line, err)
		}
		del, err := strconv.ParseBool(strings.TrimSpace(rec[cols["deleted"]]))
		if err != nil {
			return nil, fmt.Errorf("hashes csv line %d: deleted: %w", line, err)
		}
		if seen[h] {
			return nil, fmt.Errorf("hashes csv line %d: hash %s repeated", line, h)
		}
		seen[h] = true
		out = append(out, CodeHash{Hash: h, Deleted: del})
	}
}

// instanceLimits bounds a contract_data_xdr value. Instance storage can be
// large, so the cap is generous but finite.
var instanceLimits = ingest.XDRLimits{MaxBase64Len: 16 << 20}

// ReadInstances reads Hubble Q2 (contract_id, contract_data_xdr, ...) and
// decodes each instance's executable. Rows that fail to decode are counted
// and skipped; the count is reported.
func ReadInstances(r io.Reader) (contracts []Contract, decodeErrors int, err error) {
	cr, cols, err := csvReader(r, "contract_id", "contract_data_xdr")
	if err != nil {
		return nil, 0, fmt.Errorf("instances csv: %w", err)
	}
	seen := map[string]bool{}
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return contracts, decodeErrors, nil
		}
		if err != nil {
			return nil, 0, fmt.Errorf("instances csv line %d: %w", line, err)
		}
		cd, err := ingest.DecodeContractDataBase64(rec[cols["contract_data_xdr"]], instanceLimits)
		if err != nil {
			decodeErrors++
			continue
		}
		inst, err := ingest.DecodeInstance(cd)
		if err != nil {
			decodeErrors++
			continue
		}
		if want := strings.TrimSpace(rec[cols["contract_id"]]); inst.ContractID != want {
			return nil, 0, fmt.Errorf("instances csv line %d: contract_id %s but XDR is for %s", line, want, inst.ContractID)
		}
		if seen[inst.ContractID] {
			return nil, 0, fmt.Errorf("instances csv line %d: contract %s repeated", line, inst.ContractID)
		}
		seen[inst.ContractID] = true
		contracts = append(contracts, fromInstance(inst))
	}
}

func fromInstance(in ingest.Instance) Contract {
	return Contract{ID: in.ContractID, Kind: in.Kind, WasmHash: in.WasmHash, RefOwner: in.RefOwner, RefTag: in.RefTag, SACAsset: in.SACAsset}
}

// ReadExecRefs reads Hubble Q3 (contract_id, contract_data_xdr, ...).
func ReadExecRefs(r io.Reader) (map[[2]string]string, error) {
	cr, cols, err := csvReader(r, "contract_id", "contract_data_xdr")
	if err != nil {
		return nil, fmt.Errorf("exec refs csv: %w", err)
	}
	out := map[[2]string]string{}
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("exec refs csv line %d: %w", line, err)
		}
		cd, err := ingest.DecodeContractDataBase64(rec[cols["contract_data_xdr"]], instanceLimits)
		if err != nil {
			return nil, fmt.Errorf("exec refs csv line %d: %w", line, err)
		}
		ref, err := ingest.DecodeExecRef(cd)
		if err != nil {
			return nil, fmt.Errorf("exec refs csv line %d: %w", line, err)
		}
		out[[2]string{ref.Owner, ref.Tag}] = ref.WasmHash
	}
}

// ResolveRefs sets WasmHash for every wasm_ref contract. References missing
// from known are fetched with getLedgerEntries; any still missing, or
// archived, leave the contract Unresolved.
func ResolveRefs(ctx context.Context, c ingest.RPC, contracts []Contract, known map[[2]string]string) error {
	if known == nil {
		known = map[[2]string]string{}
	}
	need := map[[2]string]string{} // ref -> base64 key
	for _, ct := range contracts {
		ref := [2]string{ct.RefOwner, ct.RefTag}
		if ct.Kind != ingest.KindWasmRef {
			continue
		}
		if _, ok := known[ref]; ok {
			continue
		}
		k, err := ingest.ExecRefKey(ct.RefOwner, ct.RefTag)
		if err != nil {
			return err
		}
		need[ref] = k
	}
	keyToRef := map[string][2]string{}
	var keys []string
	for ref, k := range need {
		keyToRef[k] = ref
		keys = append(keys, k)
	}
	for start := 0; start < len(keys); start += rpc.MaxLedgerEntryKeys {
		res, err := c.GetLedgerEntries(ctx, keys[start:min(start+rpc.MaxLedgerEntryKeys, len(keys))])
		if err != nil {
			return fmt.Errorf("fetch exec refs: %w", err)
		}
		for _, e := range res.Entries {
			if e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq < res.LatestLedger {
				continue // archived: stays unresolved
			}
			data, err := ingest.DecodeLedgerEntryDataBase64(e.XDR, instanceLimits)
			if err != nil || data.Type != xdr.LedgerEntryTypeContractData {
				return fmt.Errorf("exec ref entry %s: undecodable (%v)", e.Key, err)
			}
			ref, err := ingest.DecodeExecRef(*data.ContractData)
			if err != nil {
				return fmt.Errorf("exec ref entry %s: %w", e.Key, err)
			}
			if keyToRef[e.Key] == [2]string{ref.Owner, ref.Tag} {
				known[[2]string{ref.Owner, ref.Tag}] = ref.WasmHash
			}
		}
	}
	for i := range contracts {
		ct := &contracts[i]
		if ct.Kind != ingest.KindWasmRef {
			continue
		}
		if h, ok := known[[2]string{ct.RefOwner, ct.RefTag}]; ok {
			ct.WasmHash = h
		} else {
			ct.Unresolved = true
		}
	}
	return nil
}
