package phase0

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// fixtureCode loads golden Wasm and indexes it by its hash (sha256).
func fixtureCode(t *testing.T, names ...string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, n := range names {
		b, err := fs.ReadFile(os.DirFS("../../testdata/wasm"), n)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		out[n] = b
		out[hex.EncodeToString(sum[:])] = b
	}
	return out
}

func hashOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func cid(n byte) string {
	var raw [32]byte
	raw[0] = n
	s, _ := strkey.Encode(strkey.VersionByteContract, raw[:])
	return s
}

func addr(t *testing.T, id string) xdr.ScAddress {
	raw, err := strkey.Decode(strkey.VersionByteContract, id)
	if err != nil {
		t.Fatal(err)
	}
	var c xdr.ContractId
	copy(c[:], raw)
	return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &c}
}

func instanceCD(t *testing.T, id string, ex xdr.ContractExecutable) xdr.ContractDataEntry {
	return xdr.ContractDataEntry{Contract: addr(t, id), Durability: xdr.ContractDataDurabilityPersistent,
		Key: xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
		Val: xdr.ScVal{Type: xdr.ScValTypeScvContractInstance, Instance: &xdr.ScContractInstance{Executable: ex}}}
}

func wasmExec(hash string) xdr.ContractExecutable {
	var h xdr.Hash
	b, _ := hex.DecodeString(hash)
	copy(h[:], b)
	return xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &h}
}

func refExec(t *testing.T, owner, tag string) xdr.ContractExecutable {
	return xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableExternalRef,
		ExternalRef: &xdr.ContractExecutableExternalRef{ExecutableOwner: addr(t, owner), Tag: xdr.ScString(tag)}}
}

func refCD(t *testing.T, owner, tag, hash string) xdr.ContractDataEntry {
	tg := xdr.ScString(tag)
	b, _ := hex.DecodeString(hash)
	bs := xdr.ScBytes(b)
	return xdr.ContractDataEntry{Contract: addr(t, owner), Durability: xdr.ContractDataDurabilityPersistent,
		Key: xdr.ScVal{Type: xdr.ScValTypeScvExecutableTag, ExecutableTag: &tg}, Val: xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &bs}}
}

func b64(t *testing.T, v any) string {
	s, err := xdr.MarshalBase64(v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fakeRPC serves code by hash and contract data by key.
type fakeRPC struct {
	t        *testing.T
	code     map[string][]byte                // hash -> wasm
	expired  map[string]bool                  // hash -> TTL passed
	data     map[string]xdr.ContractDataEntry // base64 key -> entry
	ledgers  []rpc.Ledger
	latest   uint32
	oldest   uint32
	passphrs string
}

func (f *fakeRPC) GetNetwork(context.Context) (rpc.Network, error) {
	return rpc.Network{Passphrase: f.passphrs}, nil
}
func (f *fakeRPC) GetLatestLedger(context.Context) (rpc.LatestLedger, error) {
	return rpc.LatestLedger{Sequence: f.latest}, nil
}
func (f *fakeRPC) GetLedgers(_ context.Context, r rpc.LedgersRequest) (rpc.LedgersPage, error) {
	page := rpc.LedgersPage{OldestLedger: f.oldest, LatestLedger: f.latest}
	for _, l := range f.ledgers {
		if l.Sequence >= r.StartLedger && len(page.Ledgers) < int(r.Limit) {
			page.Ledgers = append(page.Ledgers, l)
		}
	}
	return page, nil
}
func (f *fakeRPC) GetLedgerEntries(_ context.Context, keys []string) (rpc.LedgerEntries, error) {
	res := rpc.LedgerEntries{LatestLedger: f.latest}
	for _, k := range keys {
		var lk xdr.LedgerKey
		if err := xdr.SafeUnmarshalBase64(k, &lk); err != nil {
			f.t.Fatalf("bad key %s: %v", k, err)
		}
		live := f.latest + 100
		switch lk.Type {
		case xdr.LedgerEntryTypeContractCode:
			h := hex.EncodeToString(lk.ContractCode.Hash[:])
			code, ok := f.code[h]
			if !ok {
				continue
			}
			if f.expired[h] {
				live = f.latest - 1
			}
			d := xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.ContractCodeEntry{Hash: lk.ContractCode.Hash, Code: code}}
			res.Entries = append(res.Entries, rpc.LedgerEntry{Key: k, XDR: b64(f.t, d), LiveUntilLedgerSeq: &live})
		case xdr.LedgerEntryTypeContractData:
			cd, ok := f.data[k]
			if !ok {
				continue
			}
			d := xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractData, ContractData: &cd}
			res.Entries = append(res.Entries, rpc.LedgerEntry{Key: k, XDR: b64(f.t, d), LiveUntilLedgerSeq: &live})
		}
	}
	return res, nil
}

func testOptions(t *testing.T) Options {
	rf, err := match.LoadRules(rules.FS)
	if err != nil {
		t.Fatal(err)
	}
	return Options{Rules: rf, Limits: sepmeta.DefaultLimits(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC) }}
}

func TestRunMainnetCensus(t *testing.T) {
	code := fixtureCode(t, "token_full_sep.wasm", "token_full_legacy.wasm", "token_partial.wasm", "not_token.wasm", "multi_sep.wasm", "no_meta.wasm")
	full, legacy, partial := hashOf(code["token_full_sep.wasm"]), hashOf(code["token_full_legacy.wasm"]), hashOf(code["token_partial.wasm"])
	notTok, multi, noMeta := hashOf(code["not_token.wasm"]), hashOf(code["multi_sep.wasm"]), hashOf(code["no_meta.wasm"])
	gone := strings.Repeat("ee", 32)    // in the code census but RPC has nothing: archived
	expired := strings.Repeat("dd", 32) // RPC returns it with a passed TTL: archived
	code[expired] = code["not_token.wasm"]
	deleted := strings.Repeat("cc", 32) // marked deleted: outside the population

	hashesCSV := "contract_code_hash,deleted,last_modified_ledger,closed_at\n"
	for _, h := range []string{full, legacy, partial, notTok, multi, noMeta, gone, expired} {
		hashesCSV += h + ",false,1,2026-01-01\n"
	}
	hashesCSV += deleted + ",true,1,2026-01-01\n"

	sac := xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableStellarAsset}
	owner := cid(200)
	rows := []struct {
		id string
		cd xdr.ContractDataEntry
	}{
		{cid(1), instanceCD(t, cid(1), wasmExec(full))}, // three contracts from one factory hash
		{cid(2), instanceCD(t, cid(2), wasmExec(full))},
		{cid(3), instanceCD(t, cid(3), wasmExec(full))},
		{cid(4), instanceCD(t, cid(4), wasmExec(legacy))}, // undeclared gap
		{cid(5), instanceCD(t, cid(5), wasmExec(notTok))}, // false claim
		{cid(6), instanceCD(t, cid(6), sac)},
		{cid(7), instanceCD(t, cid(7), refExec(t, owner, "v1"))},   // resolved via Q3 -> partial
		{cid(8), instanceCD(t, cid(8), refExec(t, owner, "v2"))},   // resolved via RPC -> multi
		{cid(9), instanceCD(t, cid(9), refExec(t, owner, "gone"))}, // unresolved
		{cid(10), instanceCD(t, cid(10), wasmExec(gone))},          // runs archived code
	}
	instCSV := "contract_id,contract_data_xdr,asset_code,asset_issuer,asset_type,last_modified_ledger\n"
	for _, r := range rows {
		instCSV += fmt.Sprintf("%s,%s,,,,1\n", r.id, b64(t, r.cd))
	}
	instCSV += cid(11) + ",AAAA,,,,1\n" // undecodable row: counted, not fatal
	refsCSV := "contract_id,contract_data_xdr,last_modified_ledger\n" + owner + "," + b64(t, refCD(t, owner, "v1", partial)) + ",1\n"

	v2key, err := ingest.ExecRefKey(owner, "v2")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRPC{t: t, code: code, expired: map[string]bool{expired: true}, latest: 1000,
		data: map[string]xdr.ContractDataEntry{v2key: refCD(t, owner, "v2", multi)}}
	res, err := RunMainnet(context.Background(), f, MainnetInputs{
		Hashes: strings.NewReader(hashesCSV), Instances: strings.NewReader(instCSV), ExecRefs: strings.NewReader(refsCSV),
	}, testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summary
	check := func(name string, got, want int) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	check("contracts.total", s.Contracts.Total, 10)
	check("contracts.sac", s.Contracts.SAC, 1)
	check("contracts.wasm", s.Contracts.Wasm, 6)
	check("contracts.wasm_ref", s.Contracts.WasmRef, 3)
	check("contracts.wasm_ref_distinct", s.Contracts.WasmRefDistinct, 3)
	check("contracts.wasm_ref_unresolved", s.Contracts.WasmRefUnresolved, 1)
	check("contracts.instance_decode_errors", s.Contracts.InstanceDecodeErrors, 1)
	check("contracts.measured", s.Contracts.Measured, 7) // 1,2,3,4,5,7,8 (10 runs archived code)
	check("hashes.code_entries", s.Hashes.CodeEntries, 9)
	check("hashes.code_deleted", s.Hashes.CodeDeleted, 1)
	check("hashes.population", s.Hashes.Population, 8)
	check("hashes.archived", s.Hashes.Archived, 2)
	check("hashes.measured", s.Hashes.Measured, 6)
	check("hashes.parse_ok", s.Hashes.ParseOK, 6)
	check("hashes.used_by_contract", s.Hashes.UsedByContract, 5) // all but no_meta
	// Declaring hashes: full(41), partial(41), not_token(41), multi(41,40) = 4 of 6.
	check("declares.by_hash.n", s.DeclaresAny.ByHash.N, 4)
	check("declares.by_hash.of", s.DeclaresAny.ByHash.Of, 6)
	// Declaring contracts: 1,2,3 (full), 5 (not_token), 7 (partial), 8 (multi) = 6 of 7.
	check("declares.by_contract.n", s.DeclaresAny.ByContract.N, 6)
	check("declares.by_contract.of", s.DeclaresAny.ByContract.Of, 7)
	check("with_spec", s.WithSpec.N, 6)
	wantSEP := map[int]Pair{41: {Hashes: 4, Contracts: 6}, 40: {Hashes: 1, Contracts: 1}}
	for _, c := range s.PerSEP {
		if c.Pair != wantSEP[c.SEP] {
			t.Errorf("per_sep %d = %+v, want %+v", c.SEP, c.Pair, wantSEP[c.SEP])
		}
	}
	if len(s.PerSEP) != 2 {
		t.Errorf("per_sep = %+v", s.PerSEP)
	}
	wantStatus := map[string]Pair{"match": {2, 4}, "partial": {1, 1}, "mismatch": {3, 2}, "no_spec": {0, 0}}
	for st, want := range wantStatus {
		if got := s.SEP41.Status[st]; got != want {
			t.Errorf("sep41 %s = %+v, want %+v", st, got, want)
		}
	}
	if s.SEP41.UndeclaredGap != (Pair{Hashes: 1, Contracts: 1}) {
		t.Errorf("undeclared gap = %+v, want legacy hash and contract 4", s.SEP41.UndeclaredGap)
	}
	if got := s.SEP41.Declared41["mismatch"]; got != (Pair{Hashes: 2, Contracts: 2}) {
		t.Errorf("declared 41 but mismatch = %+v, want not_token+multi", got)
	}
	// OK histogram: full 10, legacy 10, partial 8, not_token 0, multi 1, no_meta 0.
	if h := s.SEP41.OKHistogram; len(h) != 11 || h[10] != 2 || h[8] != 1 || h[1] != 1 || h[0] != 2 {
		t.Errorf("ok histogram = %v", h)
	}
	if !strings.Contains(s.Decision, "≥ 5%") {
		t.Errorf("decision = %q (4/6 hashes declare)", s.Decision)
	}

	dir := t.TempDir()
	if err := Write(dir, res); err != nil {
		t.Fatal(err)
	}
	out := os.DirFS(dir)
	raw, _ := fs.ReadFile(out, "mainnet-raw.csv")
	if n := strings.Count(string(raw), "\n"); n != 9 { // header + 8 population hashes
		t.Errorf("raw csv has %d lines, want 9", n)
	}
	if !strings.Contains(string(raw), full+",6923,3,ok,") {
		t.Errorf("raw csv lacks the factory hash row with 3 contracts:\n%s", raw)
	}
	md, _ := fs.ReadFile(out, "ADOPTION.md")
	for _, want := range []string{"## Decision", "| Wasm contracts whose code is archived or missing | 1 | |",
		"| Code entries in census | 9 |", "| Archived or missing (not fetched) | 2 |", "that RPC returned live", "4 / 6 (66.67%)", "6 / 7 (85.71%)", "**Undeclared gap** (inferred): 1 hashes / 1 contracts"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("ADOPTION.md lacks %q", want)
		}
	}
}

func TestReadInputsRejectBadFiles(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{"hashes missing column", func() error { _, err := ReadCodeHashes(strings.NewReader("hash\nab\n")); return err }},
		{"hashes bad hash", func() error {
			_, err := ReadCodeHashes(strings.NewReader("contract_code_hash,deleted\nXYZ,false\n"))
			return err
		}},
		{"hashes repeated", func() error {
			h := strings.Repeat("ab", 32)
			_, err := ReadCodeHashes(strings.NewReader("contract_code_hash,deleted\n" + h + ",false\n" + h + ",false\n"))
			return err
		}},
		{"instances missing column", func() error { _, _, err := ReadInstances(strings.NewReader("contract_id\nC\n")); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.run() == nil {
				t.Fatal("want error")
			}
		})
	}
	t.Run("instance id must match its XDR", func(t *testing.T) {
		row := cid(2) + "," + b64(t, instanceCD(t, cid(1), wasmExec(strings.Repeat("ab", 32)))) + "\n"
		if _, _, err := ReadInstances(strings.NewReader("contract_id,contract_data_xdr\n" + row)); err == nil {
			t.Fatal("want mismatch error")
		}
	})
	t.Run("BOM in header is tolerated", func(t *testing.T) {
		got, err := ReadCodeHashes(strings.NewReader("\ufeffcontract_code_hash,deleted\n" + strings.Repeat("ab", 32) + ",FALSE\n"))
		if err != nil || len(got) != 1 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}

// realLedger is a recorded testnet LedgerCloseMeta (v2), used as a valid
// template so synthetic ledgers carry a well-formed header and tx set.
func realLedger(t *testing.T) xdr.LedgerCloseMeta {
	t.Helper()
	b, err := os.ReadFile("../rpc/testdata/getLedgers.json")
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(b), `"metadataXdr":"`)
	j := strings.Index(string(b[i+15:]), `"`)
	var m xdr.LedgerCloseMeta
	if err := xdr.SafeUnmarshalBase64(string(b[i+15:i+15+j]), &m); err != nil || m.V != 2 {
		t.Fatalf("template ledger: v%d, %v", m.V, err)
	}
	return m
}

// ledgerWithInstances returns the template ledger renumbered to seq, with
// its transactions replaced by one that updates the given instances.
func ledgerWithInstances(t *testing.T, tmpl xdr.LedgerCloseMeta, seq uint32, cds ...xdr.ContractDataEntry) rpc.Ledger {
	var changes xdr.LedgerEntryChanges
	for i := range cds {
		e := xdr.LedgerEntry{Data: xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractData, ContractData: &cds[i]}}
		changes = append(changes, xdr.LedgerEntryChange{Type: xdr.LedgerEntryChangeTypeLedgerEntryUpdated, Updated: &e})
	}
	v2 := *tmpl.V2
	v2.LedgerHeader.Header.LedgerSeq = xdr.Uint32(seq)
	v2.TxProcessing = []xdr.TransactionResultMetaV1{{
		Result:            tmpl.V2.TxProcessing[0].Result,
		TxApplyProcessing: xdr.TransactionMeta{V: 4, V4: &xdr.TransactionMetaV4{Operations: []xdr.OperationMetaV2{{Changes: changes}}}},
	}}
	v2.UpgradesProcessing, v2.EvictedKeys = nil, nil
	return rpc.Ledger{Sequence: seq, MetadataXDR: b64(t, xdr.LedgerCloseMeta{V: 2, V2: &v2})}
}

func TestRunTestnetSample(t *testing.T) {
	code := fixtureCode(t, "token_full_sep.wasm", "token_full_legacy.wasm")
	full, legacy := hashOf(code["token_full_sep.wasm"]), hashOf(code["token_full_legacy.wasm"])
	f := &fakeRPC{t: t, code: code, oldest: 101, latest: 140, data: map[string]xdr.ContractDataEntry{}}
	// Ledgers 101..140; 4 strata of 10. Each stratum's first ledger touches 3
	// contracts; the quota is ceil(6/4) = 2, so each stratum contributes 2
	// until the target of 6 is reached in stratum 3.
	tmpl := realLedger(t)
	n := byte(1)
	for seq := uint32(101); seq <= 140; seq++ {
		var cds []xdr.ContractDataEntry
		if (seq-101)%10 == 0 {
			for k := 0; k < 3; k++ {
				id := cid(n)
				h := full
				if n%2 == 0 {
					h = legacy
				}
				cd := instanceCD(t, id, wasmExec(h))
				cds = append(cds, cd)
				key, err := ingest.InstanceKey(id)
				if err != nil {
					t.Fatal(err)
				}
				if n != 4 { // contract 4 is drawn but has since been archived
					f.data[key] = cd
				}
				n++
			}
		}
		f.ledgers = append(f.ledgers, ledgerWithInstances(t, tmpl, seq, cds...))
	}
	res, err := RunTestnet(context.Background(), f, SampleConfig{Target: 6, Strata: 4, PageSize: 5}, testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summary
	if s.Sample == nil || fmt.Sprint(s.Sample.PerStratum) != "[2 2 2]" || s.Sample.OldestLedger != 101 {
		t.Fatalf("sample = %+v", s.Sample)
	}
	if s.Contracts.Total != 6 || s.Contracts.ArchivedInstances != 1 || s.Contracts.Measured != 5 {
		t.Fatalf("contracts = %+v", s.Contracts)
	}
	// Each stratum sees 3 contracts and takes the first 2: ids 1,2 | 4,5 | 7,8.
	// 4 is archived. Odd ids run token_full_sep (declares 41), even ids run
	// token_full_legacy (declares nothing): 1,5,7 of the 5 measured declare.
	if s.DeclaresAny.ByContract != rate(3, 5) || s.DeclaresAny.Wilson95 == nil {
		t.Fatalf("declares = %+v", s.DeclaresAny)
	}
	if s.Decision != "" {
		t.Fatalf("testnet must not carry the mainnet decision row: %q", s.Decision)
	}
}

func TestWilson(t *testing.T) {
	tests := []struct {
		n, of     int
		low, high float64
	}{
		{0, 10, 0, 27.75},
		{5, 10, 23.66, 76.34},
		{10, 10, 72.25, 100},
		{20, 2000, 0.65, 1.54},
	}
	for _, tt := range tests {
		got := Wilson(tt.n, tt.of)
		if math.Abs(got.Low-tt.low) > 0.01 || math.Abs(got.High-tt.high) > 0.01 {
			t.Errorf("Wilson(%d,%d) = %.2f..%.2f, want %.2f..%.2f", tt.n, tt.of, got.Low, got.High, tt.low, tt.high)
		}
	}
}

func TestDecide(t *testing.T) {
	for _, tt := range []struct {
		pct  float64
		want string
	}{{5, "≥ 5%"}, {4.99, "1% to < 5%"}, {1, "1% to < 5%"}, {0.99, "< 1%"}, {0, "< 1%"}} {
		if got := Decide(tt.pct); !strings.HasPrefix(got, tt.want) {
			t.Errorf("Decide(%v) = %q, want prefix %q", tt.pct, got, tt.want)
		}
	}
}

func TestAnalyzeTruncatedIsError(t *testing.T) {
	code := fixtureCode(t, "truncated.wasm")
	r := ingest.Analyze("x", code["truncated.wasm"], testOptions(t).Rules, match.Matcher{}, sepmeta.DefaultLimits())
	if r.ParseStatus != ingest.ParseError || len(r.Claims.SEPs) != 0 || r.Matches[0].Status != match.StatusNoSpec {
		t.Fatalf("got %+v", r)
	}
}
