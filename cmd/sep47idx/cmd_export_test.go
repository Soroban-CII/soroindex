package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
)

// exportFixture extends the read fixture so every export table has a row:
// a current version, a match, an anomaly, a verification and a SAC.
func exportFixture(t *testing.T) (db, id, hash string) {
	t.Helper()
	db, id, hash = readFixture(t)
	ctx := context.Background()
	s, err := store.Open(ctx, db, store.Options{Passphrase: config.Testnet.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	versions, err := loadedRulesets()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.UpsertMatch(ctx, store.MatchRow{Hash: hash, SEP: 41, RulesetVersion: versions[41],
		RuleFileSHA256: strings.Repeat("b", 64), Status: "match", OKCount: 10,
		MissingJSON: "[]", MismatchedJSON: "[]", MatchedVariantsJSON: "{}", ComputedAt: "2026-11-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.ReplaceAnomalies(ctx, hash, []store.AnomalyRow{{RawToken: "abc", Anomaly: "non_numeric"}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.UpsertContract(ctx, store.ContractRow{ID: "CSAC", Kind: "sac", SACAsset: "native"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO verifications (contract_id,wasm_hash,sep,tool,tool_version,verdict,passed,clauses_json,run_at)
		VALUES (?,?,41,'soroban-guard','0.4.2','pass',1,'[]','2026-11-01T00:00:00Z')`, id, hash); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return db, id, hash
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- test-owned export directory
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestExportWritesChecksummedBundle(t *testing.T) {
	db, id, _ := exportFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	out := t.TempDir()

	var stdout, stderr bytes.Buffer
	if code := run([]string{"export", "--out", out}, &stdout, &stderr); code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Wrote 6 files") || !strings.Contains(stdout.String(), "contracts.csv") {
		t.Fatalf("output = %s", stdout.String())
	}

	var m exportManifest
	raw, err := os.ReadFile(filepath.Join(out, "manifest.json")) // #nosec G304 -- test-owned export directory
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	wantSchema, err := store.SchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if m.Format != "sep47idx-export" || m.FormatVersion != 1 || m.Network != "testnet" || m.LastLedger != 10 ||
		m.SchemaVersion != wantSchema || m.ParserVersion == "" || m.RulesetVersions[41] == "" {
		t.Fatalf("manifest = %+v", m)
	}
	if len(m.Files) != len(store.ExportTables) {
		t.Fatalf("manifest lists %d files, want %d", len(m.Files), len(store.ExportTables))
	}

	rowsByTable := map[string][][]string{}
	for i, f := range m.Files {
		table := store.ExportTables[i]
		if f.Name != string(table)+".csv" {
			t.Fatalf("file %d = %q, want %q", i, f.Name, string(table)+".csv")
		}
		path := filepath.Join(out, f.Name)
		data, err := os.ReadFile(path) // #nosec G304 -- test-owned export directory
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != f.SHA256 {
			t.Fatalf("%s: manifest sha256 %s, file is %s", f.Name, f.SHA256, got)
		}
		rows := readCSV(t, path)
		if len(rows) == 0 || strings.Join(rows[0], ",") != strings.Join(table.Columns(), ",") {
			t.Fatalf("%s header = %v, want %v", f.Name, rows[0], table.Columns())
		}
		if int64(len(rows)-1) != f.Rows {
			t.Fatalf("%s: manifest says %d rows, file has %d", f.Name, f.Rows, len(rows)-1)
		}
		rowsByTable[string(table)] = rows
	}

	// The fixture's contract declares, matches and passed SEP-41; the SAC is
	// protocol-only. Tiers must not leak between columns.
	var matchRow, sacRow []string
	for _, row := range rowsByTable["contracts"][1:] {
		switch row[0] {
		case id:
			matchRow = row
		case "CSAC":
			sacRow = row
		}
	}
	if matchRow == nil || sacRow == nil {
		t.Fatalf("contracts rows = %v", rowsByTable["contracts"])
	}
	if matchRow[9] != "41" || matchRow[10] != "41" || matchRow[11] != "41" || matchRow[12] != "" {
		t.Fatalf("declaring contract tier columns = declared %q inferred %q verified %q protocol %q",
			matchRow[9], matchRow[10], matchRow[11], matchRow[12])
	}
	if sacRow[9] != "" || sacRow[10] != "" || sacRow[11] != "" || sacRow[12] != "41" {
		t.Fatalf("SAC tier columns = declared %q inferred %q verified %q protocol %q",
			sacRow[9], sacRow[10], sacRow[11], sacRow[12])
	}
	if len(rowsByTable["anomalies"]) != 2 || len(rowsByTable["matches"]) != 2 || len(rowsByTable["versions"]) != 2 || len(rowsByTable["claims"]) != 2 {
		t.Fatalf("table row counts = anomalies %d matches %d versions %d claims %d",
			len(rowsByTable["anomalies"])-1, len(rowsByTable["matches"])-1, len(rowsByTable["versions"])-1, len(rowsByTable["claims"])-1)
	}
}

func TestExportIsByteIdentical(t *testing.T) {
	db, _, _ := exportFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	first, second := t.TempDir(), t.TempDir()
	for _, out := range []string{first, second} {
		if code := run([]string{"export", "--out", out}, &bytes.Buffer{}, &bytes.Buffer{}); code != exitOK {
			t.Fatalf("export to %s: code %d", out, code)
		}
	}
	for _, table := range store.ExportTables {
		name := string(table) + ".csv"
		a, err := os.ReadFile(filepath.Join(first, name)) // #nosec G304 -- test-owned export directories
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(second, name)) // #nosec G304 -- test-owned export directories
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Fatalf("%s differs between two exports of an unchanged index", name)
		}
	}
	a, err := os.ReadFile(filepath.Join(first, "manifest.json")) // #nosec G304 -- test-owned export directory
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(second, "manifest.json")) // #nosec G304 -- test-owned export directory
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("manifest.json differs between two exports of an unchanged index")
	}
}

func TestExportRejectsBadInput(t *testing.T) {
	db, _, _ := exportFixture(t)
	t.Setenv("SEP47IDX_DB", db)
	for _, args := range [][]string{
		{"export"},
		{"export", "--out", t.TempDir(), "extra"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitError {
			t.Fatalf("%v: code = %d, stderr = %s", args, code, errb.String())
		}
	}
}
