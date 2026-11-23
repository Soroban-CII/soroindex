package ingest

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// fuzzMetaLimit bounds one fuzz input. The recorded ledgers are about 200 KB
// of base64 each; 1 MiB leaves room for mutations while still exercising the
// limit path.
const fuzzMetaLimit = 1 << 20

// FuzzExtractContractFacts mutates recorded LedgerCloseMeta XDR and runs the
// same decode and extraction path sync uses. Network metadata is untrusted:
// an over-limit input must fail with ErrTooLarge before decoding, and any
// ledger that decodes must yield facts that match its own changes.
func FuzzExtractContractFacts(f *testing.F) {
	seedRecordedMetadata(f)
	f.Add("")
	f.Add("AAAA")
	f.Fuzz(func(t *testing.T, metaB64 string) {
		// The length check must reject before the decoder sees the input and
		// must leave the destination untouched.
		var untouched xdr.LedgerCloseMeta
		over := strings.Repeat("A", 4097)
		if err := UnmarshalBase64(over, &untouched, XDRLimits{MaxBase64Len: 4096}); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("over-limit input: err = %v, want ErrTooLarge", err)
		}
		if untouched.V != 0 || untouched.V0 != nil || untouched.V1 != nil || untouched.V2 != nil {
			t.Fatal("over-limit input was decoded")
		}

		var m xdr.LedgerCloseMeta
		err := UnmarshalBase64(metaB64, &m, XDRLimits{MaxBase64Len: fuzzMetaLimit, MaxDepth: 500})
		if err != nil {
			if len(metaB64) > fuzzMetaLimit && !errors.Is(err, ErrTooLarge) {
				t.Fatalf("%d-byte input: err = %v, want ErrTooLarge", len(metaB64), err)
			}
			return
		}
		facts, err := ExtractContractFacts(m)
		if err != nil {
			if !errors.Is(err, ErrUnsupportedMeta) {
				t.Fatalf("unclassified extraction error: %v", err)
			}
			return
		}
		checkFactsMatchChanges(t, m, facts)
	})
}

// seedRecordedMetadata adds the real testnet ledgers recorded for the rpc
// package, so mutations start from the shapes sync actually sees.
func seedRecordedMetadata(f *testing.F) {
	f.Helper()
	b, err := os.ReadFile("../rpc/testdata/getLedgers.json")
	if err != nil {
		f.Fatal(err)
	}
	var resp struct {
		Result struct {
			Ledgers []struct {
				MetadataXDR string `json:"metadataXdr"`
			} `json:"ledgers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		f.Fatal(err)
	}
	if len(resp.Result.Ledgers) == 0 {
		f.Fatal("recorded getLedgers has no ledgers")
	}
	for _, l := range resp.Result.Ledgers {
		if l.MetadataXDR != "" {
			f.Add(l.MetadataXDR)
		}
	}
	// Small synthetic ledgers keep mutations cheap: a 200 KB seed is mostly
	// copied rather than explored.
	small := []xdr.LedgerCloseMeta{
		{V: 0, V0: &xdr.LedgerCloseMetaV0{}},
	}
	for _, m := range small {
		s, err := xdr.MarshalBase64(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(s)
	}
}

// checkFactsMatchChanges asserts every fact came from one of the ledger's own
// changes and keeps the invariants the store relies on.
func checkFactsMatchChanges(t *testing.T, m xdr.LedgerCloseMeta, f ContractFacts) {
	t.Helper()
	if f.Ledger != m.LedgerSequence() {
		t.Fatalf("facts ledger %d, meta ledger %d", f.Ledger, m.LedgerSequence())
	}
	codeCreated := map[string]bool{}
	type instanceChange struct {
		in      Instance
		created bool
	}
	var instances []instanceChange
	var refs []ExecRef
	evicted := 0
	for _, c := range f.Changes {
		if c.Kind == Evicted {
			if c.Key == nil {
				t.Fatal("evicted change without a key")
			}
			evicted++
			continue
		}
		if c.Kind == Removed {
			continue
		}
		if c.Entry == nil {
			t.Fatal("non-removal change without an entry")
		}
		switch c.Entry.Data.Type {
		case xdr.LedgerEntryTypeContractCode:
			if c.Kind == Created || c.Kind == Restored {
				codeCreated[hex.EncodeToString(c.Entry.Data.ContractCode.Hash[:])] = true
			}
		case xdr.LedgerEntryTypeContractData:
			cd := *c.Entry.Data.ContractData
			if IsInstanceKey(cd) {
				in, err := DecodeInstance(cd)
				if err != nil {
					t.Fatalf("change carried an undecodable instance: %v", err)
				}
				instances = append(instances, instanceChange{in: in, created: c.Kind == Created})
			}
			if IsExecRefKey(cd) {
				ref, err := DecodeExecRef(cd)
				if err != nil {
					t.Fatalf("change carried an undecodable reference: %v", err)
				}
				refs = append(refs, ref)
			}
		}
	}
	if len(codeCreated) != len(f.CodeCreated) {
		t.Fatalf("CodeCreated has %d entries, changes have %d", len(f.CodeCreated), len(codeCreated))
	}
	for _, h := range f.CodeCreated {
		if !codeCreated[h] {
			t.Fatalf("CodeCreated hash %q is not a created or restored code change", h)
		}
	}
	if len(instances) != len(f.Instances) {
		t.Fatalf("Instances has %d entries, changes have %d", len(f.Instances), len(instances))
	}
	for i, want := range instances {
		got := f.Instances[i]
		if got.Instance != want.in || got.Created != want.created {
			t.Fatalf("instance %d = %+v, change says %+v", i, got, want)
		}
		checkInstance(t, got.Instance)
	}
	if len(refs) != len(f.ExecRefs) {
		t.Fatalf("ExecRefs has %d entries, changes have %d", len(f.ExecRefs), len(refs))
	}
	for i, want := range refs {
		if f.ExecRefs[i] != want {
			t.Fatalf("exec ref %d = %+v, change says %+v", i, f.ExecRefs[i], want)
		}
	}
	if evicted != len(f.Evicted) {
		t.Fatalf("Evicted has %d entries, changes have %d", len(f.Evicted), evicted)
	}
	for _, err := range f.Skipped {
		if !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrNotInstance) && !errors.Is(err, ErrNotExecRef) {
			t.Fatalf("unclassified skipped entry: %v", err)
		}
	}
}

func checkInstance(t *testing.T, in Instance) {
	t.Helper()
	if in.ContractID == "" || in.Kind == "" {
		t.Fatalf("incomplete instance %+v", in)
	}
	switch in.Kind {
	case KindWasm:
		if len(in.WasmHash) != 64 || strings.ToLower(in.WasmHash) != in.WasmHash {
			t.Fatalf("wasm hash %q is not 64 lowercase hex characters", in.WasmHash)
		}
		if in.RefOwner != "" || in.RefTag != "" {
			t.Fatalf("wasm instance carries a reference: %+v", in)
		}
	case KindWasmRef:
		if in.RefOwner == "" || in.RefTag == "" || in.WasmHash != "" {
			t.Fatalf("reference instance %+v", in)
		}
	case KindSAC:
		if in.WasmHash != "" || in.RefOwner != "" || in.RefTag != "" {
			t.Fatalf("SAC instance carries code or a reference: %+v", in)
		}
	default:
		t.Fatalf("unknown kind %q", in.Kind)
	}
}
