package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Soroban-CII/soroindex/internal/claims"
	"github.com/Soroban-CII/soroindex/internal/match"
	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
	"github.com/Soroban-CII/soroindex/rules"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func seedCID(n byte) string {
	var raw [32]byte
	raw[0] = n
	s, _ := strkey.Encode(strkey.VersionByteContract, raw[:])
	return s
}

// entryRPC serves ledger entries from maps keyed by base64 LedgerKey.
type entryRPC struct {
	t       *testing.T
	latest  uint32
	code    map[string][]byte
	data    map[string]xdr.ContractDataEntry
	lastMod map[string]uint32
	expired map[string]bool
	calls   int
}

func (f *entryRPC) GetNetwork(context.Context) (rpc.Network, error) { return rpc.Network{}, nil }
func (f *entryRPC) GetLatestLedger(context.Context) (rpc.LatestLedger, error) {
	return rpc.LatestLedger{Sequence: f.latest}, nil
}
func (f *entryRPC) GetLedgers(context.Context, rpc.LedgersRequest) (rpc.LedgersPage, error) {
	return rpc.LedgersPage{}, nil
}
func (f *entryRPC) GetLedgerEntries(_ context.Context, keys []string) (rpc.LedgerEntries, error) {
	f.calls++
	res := rpc.LedgerEntries{LatestLedger: f.latest}
	for _, k := range keys {
		var lk xdr.LedgerKey
		if err := xdr.SafeUnmarshalBase64(k, &lk); err != nil {
			f.t.Fatal(err)
		}
		live := f.latest + 10
		if f.expired[k] {
			live = f.latest - 1
		}
		var d xdr.LedgerEntryData
		switch lk.Type {
		case xdr.LedgerEntryTypeContractCode:
			code, ok := f.code[hex.EncodeToString(lk.ContractCode.Hash[:])]
			if !ok {
				continue
			}
			d = xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.ContractCodeEntry{Hash: lk.ContractCode.Hash, Code: code}}
		case xdr.LedgerEntryTypeContractData:
			cd, ok := f.data[k]
			if !ok {
				continue
			}
			d = xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractData, ContractData: &cd}
		}
		s, err := xdr.MarshalBase64(d)
		if err != nil {
			f.t.Fatal(err)
		}
		res.Entries = append(res.Entries, rpc.LedgerEntry{Key: k, XDR: s, LastModifiedLedgerSeq: f.lastMod[k], LiveUntilLedgerSeq: &live})
	}
	return res, nil
}

func newIndexer(t *testing.T, r RPC) (*Indexer, *store.Store) {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "i.db"), store.Options{Passphrase: "Test SDF Network ; September 2015"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	rf, err := match.LoadRules(rules.FS)
	if err != nil {
		t.Fatal(err)
	}
	return &Indexer{Store: s, RPC: r, Rules: rf, Limits: sepmeta.DefaultLimits(), Claims: claims.Default(sepmeta.DefaultLimits()),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC) }}, s
}

func wasmFixture(t *testing.T, name string) ([]byte, string) {
	b, err := fs.ReadFile(os.DirFS("../../testdata/wasm"), name)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:])
}

func execWasm(hash string) xdr.ContractExecutable {
	var h xdr.Hash
	b, _ := hex.DecodeString(hash)
	copy(h[:], b)
	return xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &h}
}

// dump returns every row of every table, for replay comparison.
func dump(t *testing.T, s *store.Store) string {
	t.Helper()
	var b strings.Builder
	for _, tbl := range []string{"contracts", "wasm", "contract_versions", "exec_refs", "wasm_claims", "parse_anomalies", "interface_matches", "sync_state"} {
		rows, err := s.DB.Query("SELECT * FROM " + tbl + " ORDER BY 1, 2") // #nosec G202 -- fixed table names in a test
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s %v\n", tbl, vals)
		}
		_ = rows.Close()
	}
	return b.String()
}

func TestSeed(t *testing.T) {
	ctx := context.Background()
	fullCode, full := wasmFixture(t, "token_full_sep.wasm")
	partialCode, partial := wasmFixture(t, "token_partial.wasm")
	notTokCode, notTok := wasmFixture(t, "not_token.wasm")
	owner := seedCID(90)
	f := &entryRPC{t: t, latest: 5000, code: map[string][]byte{full: fullCode, partial: partialCode, notTok: notTokCode},
		data: map[string]xdr.ContractDataEntry{}, lastMod: map[string]uint32{}, expired: map[string]bool{}}
	put := func(id string, ex xdr.ContractExecutable, lastMod uint32) string {
		k, err := InstanceKey(id)
		if err != nil {
			t.Fatal(err)
		}
		f.data[k] = instanceEntry(t, ex, nil)
		cd := f.data[k]
		cd.Contract = contractAddr(t, id)
		f.data[k] = cd
		f.lastMod[k] = lastMod
		return k
	}
	put(seedCID(1), execWasm(full), 100)
	put(seedCID(2), execWasm(full), 110) // same factory hash
	put(seedCID(3), execWasm(notTok), 120)
	put(seedCID(4), xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableStellarAsset}, 130)
	put(seedCID(5), xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableExternalRef,
		ExternalRef: &xdr.ContractExecutableExternalRef{ExecutableOwner: contractAddr(t, owner), Tag: "v1"}}, 140)
	archivedKey := put(seedCID(6), execWasm(full), 150)
	f.expired[archivedKey] = true
	refKey, _ := ExecRefKey(owner, "v1")
	tag := xdr.ScString("v1")
	hb, _ := hex.DecodeString(partial)
	bs := xdr.ScBytes(hb)
	f.data[refKey] = xdr.ContractDataEntry{Contract: contractAddr(t, owner), Durability: xdr.ContractDataDurabilityPersistent,
		Key: xdr.ScVal{Type: xdr.ScValTypeScvExecutableTag, ExecutableTag: &tag}, Val: xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &bs}}

	csvIn := "contract_id,wasm_hash\n"
	for i := byte(1); i <= 7; i++ { // 7 is not deployed
		csvIn += seedCID(i) + ",\n"
	}
	csvIn += seedCID(3) + "," + full + "\n" // duplicate row with a wrong hint: dropped as duplicate

	ix, s := newIndexer(t, f)
	st, err := ix.Seed(ctx, SeedFileSource{R: strings.NewReader(csvIn)})
	if err != nil {
		t.Fatal(err)
	}
	want := SeedStats{Requested: 7, Seeded: 6, NotFound: 1, Archived: 1, WasmFetched: 3, LastLedger: 5000}
	if st != want {
		t.Fatalf("stats = %+v, want %+v", st, want)
	}
	q := func(sql string) string {
		var v string
		if err := s.DB.QueryRow(sql).Scan(&v); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return v
	}
	checks := []struct{ name, sql, want string }{
		{"kinds", `SELECT group_concat(kind || ':' || n) FROM (SELECT kind, count(*) n FROM contracts GROUP BY kind ORDER BY kind)`, "sac:1,wasm:4,wasm_ref:1"},
		{"ref contract resolves to the referenced hash", `SELECT current_wasm_hash FROM contracts WHERE kind = 'wasm_ref'`, partial},
		{"archived contract is flagged, not dropped", `SELECT archived FROM contracts WHERE contract_id = '` + seedCID(6) + `'`, "1"},
		{"one open version per contract, from lastModified", `SELECT group_concat(from_ledger) FROM (SELECT from_ledger FROM contract_versions WHERE to_ledger IS NULL ORDER BY from_ledger)`, "100,110,120,130,140,150"},
		{"wasm rows, each fetched once", `SELECT count(*) FROM wasm WHERE parse_status = 'ok'`, "3"},
		{"claims for declaring wasm", `SELECT count(*) FROM wasm_claims WHERE sep = 41 AND source = 'sep47-meta'`, "3"},
		{"sep41 matches", `SELECT group_concat(status) FROM (SELECT status FROM interface_matches WHERE sep = 41 ORDER BY status)`, "match,mismatch,partial"},
		{"meta_json holds all pairs", `SELECT json_array_length(meta_json) FROM wasm WHERE hash = '` + full + `'`, "5"},
		{"exec ref recorded", `SELECT wasm_hash FROM exec_refs WHERE owner_contract_id = '` + owner + `'`, partial},
		{"last_ledger", `SELECT value FROM sync_state WHERE key = 'last_ledger'`, "5000"},
		{"claims view: current SEP-41 declarers", `SELECT count(DISTINCT contract_id) FROM claims WHERE sep = 41 AND current`, "5"},
	}
	for _, c := range checks {
		if got := q(c.sql); got != c.want {
			t.Errorf("%s = %s, want %s", c.name, got, c.want)
		}
	}

	t.Run("replaying the seed leaves the database identical and fetches no wasm", func(t *testing.T) {
		before := dump(t, s)
		st2, err := ix.Seed(ctx, SeedFileSource{R: strings.NewReader(csvIn)})
		if err != nil {
			t.Fatal(err)
		}
		if after := dump(t, s); after != before {
			t.Fatalf("database changed on replay:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		if st2.WasmFetched != 0 {
			t.Fatalf("replay fetched %d wasm", st2.WasmFetched)
		}
	})
}

func TestSeedFileSource(t *testing.T) {
	a, b := seedCID(1), seedCID(2)
	hash := strings.Repeat("ab", 32)
	tests := []struct {
		name    string
		in      string
		want    []SeedRow
		wantErr string
	}{
		{"header with hash", "contract_id,wasm_hash\n" + a + "," + hash + "\n", []SeedRow{{a, hash}}, ""},
		{"headerless id only", a + "\n" + b + "\n", []SeedRow{{a, ""}, {b, ""}}, ""},
		{"headerless id and hash", a + "," + hash + "\n", []SeedRow{{a, hash}}, ""},
		{"Hubble Q2 export columns", "contract_id,contract_data_xdr,asset_code\n" + a + ",AAAA,\n", []SeedRow{{a, ""}}, ""},
		{"duplicates dropped", "contract_id\n" + a + "\n" + a + "\n", []SeedRow{{a, ""}}, ""},
		{"no contract_id column", "id,hash\nx,y\n", nil, "no contract_id column"},
		{"invalid id names its row", "contract_id\n" + a + "\nGABC\n", nil, "row 2"},
		{"empty file", "", nil, "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SeedFileSource{R: strings.NewReader(tt.in)}.Contracts(context.Background())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}
