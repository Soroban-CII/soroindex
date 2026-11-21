package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// sampleRows are representative rows for every table, written to a database
// at an older schema version before it is migrated forward. Each row names
// columns; the harness inserts only the columns that exist at that version,
// so the same rows describe every historical schema.
//
// When a new migration adds a NOT NULL column without a default, inserting
// these rows at the new version fails and the harness says which column
// needs a sample value. That is the point: a new migration cannot ship
// without deciding what existing rows look like after it.
var sampleRows = map[string][]map[string]any{
	"sync_state": {
		{"key": KeyLastLedger, "value": "1000"},
		{"key": KeyRPCURL, "value": "https://rpc.example.invalid"},
		{"key": KeyRulesetVersions, "value": `{"41":"sep41-v0.5.2"}`},
	},
	"contracts": {
		{"contract_id": "CWASM", "kind": "wasm", "current_wasm_hash": hex64("a"), "created_ledger": 10, "updated_ledger": 20, "archived": 0},
		{"contract_id": "CUPGRADED", "kind": "wasm", "current_wasm_hash": hex64("b"), "created_ledger": 10, "updated_ledger": 30, "archived": 0},
		{"contract_id": "CREF", "kind": "wasm_ref", "current_wasm_hash": hex64("a"), "exec_ref_owner": "COWNER", "exec_ref_tag": "v1", "created_ledger": 11, "updated_ledger": 11, "archived": 0},
		{"contract_id": "CREFUNRESOLVED", "kind": "wasm_ref", "exec_ref_owner": "COWNER", "exec_ref_tag": "gone", "created_ledger": 12, "archived": 1},
		{"contract_id": "CSAC", "kind": "sac", "sac_asset": "native", "created_ledger": 5, "updated_ledger": 5, "archived": 0},
		{"contract_id": "CARCHIVED", "kind": "wasm", "current_wasm_hash": hex64("c"), "created_ledger": 7, "updated_ledger": 7, "archived": 1},
	},
	"wasm": {
		{"hash": hex64("a"), "size_bytes": 2048, "first_seen_ledger": 10, "has_meta": 1, "has_spec": 1, "sep_entry_count": 2,
			"meta_json": `[{"key":"sep","value":"41, 040"}]`, "parse_status": "ok", "parser_version": "1"},
		{"hash": hex64("b"), "size_bytes": 4096, "first_seen_ledger": 30, "has_meta": 1, "has_spec": 1, "sep_entry_count": 1,
			"meta_json": `[{"key":"sep","value":"SEP-41"}]`, "parse_status": "partial", "parse_error": "truncated entry", "parser_version": "1"},
		{"hash": hex64("c"), "size_bytes": 512, "first_seen_ledger": 7, "parse_status": "archived", "parser_version": "1"},
		{"hash": hex64("d"), "size_bytes": 9, "first_seen_ledger": 8, "parse_status": "error", "parse_error": "not wasm", "parser_version": "1"},
	},
	"contract_versions": {
		{"contract_id": "CWASM", "wasm_hash": hex64("a"), "from_ledger": 10},
		{"contract_id": "CUPGRADED", "wasm_hash": hex64("a"), "from_ledger": 10, "to_ledger": 30},
		{"contract_id": "CUPGRADED", "wasm_hash": hex64("b"), "from_ledger": 30},
		{"contract_id": "CREF", "wasm_hash": hex64("a"), "exec_ref_owner": "COWNER", "exec_ref_tag": "v1", "from_ledger": 11},
		{"contract_id": "CREFUNRESOLVED", "exec_ref_owner": "COWNER", "exec_ref_tag": "gone", "from_ledger": 12},
		{"contract_id": "CSAC", "from_ledger": 5},
	},
	"exec_refs": {
		{"owner_contract_id": "COWNER", "tag": "v1", "wasm_hash": hex64("a"), "updated_ledger": 11, "archived": 0},
		{"owner_contract_id": "COWNER", "tag": "gone", "updated_ledger": 12, "archived": 1},
	},
	// Populated by init() with real checksums so verifyChecksums() accepts them.
	"migration_checksums": {},
	"wasm_claims": {
		{"wasm_hash": hex64("a"), "sep": 41, "source": "sep47-meta", "raw_token": "41"},
		{"wasm_hash": hex64("a"), "sep": 40, "source": "sep47-meta", "raw_token": "040", "anomaly": "leading_zero"},
		{"wasm_hash": hex64("b"), "sep": 41, "source": "sep47-meta", "raw_token": "SEP-41", "anomaly": "prefixed_token"},
	},
	"parse_anomalies": {
		{"wasm_hash": hex64("a"), "raw_token": "abc", "anomaly": "non_numeric"},
		{"wasm_hash": hex64("b"), "raw_token": "", "anomaly": "empty_token"},
	},
	"interface_matches": {
		{"wasm_hash": hex64("a"), "sep": 41, "ruleset_version": "sep41-v0.5.2", "status": "match", "ok_count": 10,
			"missing_json": "[]", "mismatched_json": "[]", "matched_variants_json": `{"transfer.to":"MuxedAddress"}`, "computed_at": "2026-10-07T00:00:00Z"},
		{"wasm_hash": hex64("b"), "sep": 41, "ruleset_version": "sep41-v0.5.2", "status": "partial", "ok_count": 8,
			"missing_json": `["burn","burn_from"]`, "mismatched_json": "[]", "matched_variants_json": "{}", "computed_at": "2026-10-07T00:00:00Z"},
		{"wasm_hash": hex64("b"), "sep": 41, "ruleset_version": "sep41-v0.5.1", "status": "mismatch", "ok_count": 2,
			"missing_json": "[]", "mismatched_json": `[{"fn":"transfer"}]`, "matched_variants_json": "{}", "computed_at": "2026-09-01T00:00:00Z"},
		{"wasm_hash": hex64("d"), "sep": 41, "ruleset_version": "sep41-v0.5.2", "status": "no_spec", "ok_count": 0,
			"missing_json": "[]", "mismatched_json": "[]", "matched_variants_json": "{}", "computed_at": "2026-10-07T00:00:00Z"},
	},
	"verifications": {
		{"contract_id": "CWASM", "wasm_hash": hex64("a"), "sep": 41, "tool": "soroban-guard", "tool_version": "0.4.2",
			"verdict": "pass", "passed": 1, "clauses_json": `[{"id":"transfer","ok":true}]`, "run_at": "2026-10-08T00:00:00Z"},
		{"contract_id": "CUPGRADED", "wasm_hash": hex64("a"), "sep": 41, "tool": "soroban-guard", "tool_version": "0.4.2",
			"verdict": "fail", "passed": 0, "clauses_json": `[{"id":"burn","ok":false}]`, "run_at": "2026-10-08T00:00:00Z"},
		{"contract_id": "CREF", "wasm_hash": hex64("a"), "sep": 41, "tool": "soroban-guard", "tool_version": "0.4.2",
			"verdict": "unverifiable", "passed": 0, "clauses_json": "[]", "run_at": "2026-10-08T00:00:00Z"},
	},
}

func hex64(c string) string { return strings.Repeat(c, 64) }

// column is one column of a table as PRAGMA table_info reports it.
type column struct {
	name     string
	notNull  bool
	hasDflt  bool
	dfltText string
	pk       bool
}

func tableColumns(t *testing.T, db *sql.DB, table string) []column {
	t.Helper()
	rows, err := db.Query(`SELECT name, "notnull", dflt_value, pk FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []column
	for rows.Next() {
		var c column
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&c.name, &c.notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		c.hasDflt, c.dfltText, c.pk = dflt.Valid, dflt.String, pk > 0
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func userTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

// buildAtVersion applies migrations 1..version to a new file exactly as
// Store.migrate does, records the network, and returns the path.
func buildAtVersion(t *testing.T, ms []migration, version int) (string, *sql.DB) {
	t.Helper()
	p := filepath.Join(t.TempDir(), fmt.Sprintf("v%d.db", version))
	db, err := sql.Open("sqlite", "file:"+p+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.migrate(context.Background(), ms[:version]); err != nil {
		t.Fatalf("build schema %d: %v", version, err)
	}
	if err := s.SetState(context.Background(), KeyNetworkPassphrase, testnet); err != nil {
		t.Fatal(err)
	}
	return p, db
}

// seed writes sampleRows using only the columns present at this version.
func seed(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	tables := userTables(t, db)
	for _, table := range tables {
		rows, ok := sampleRows[table]
		if !ok {
			t.Fatalf("schema %d has table %s with no sampleRows entry: add representative rows for it", version, table)
		}
		cols := tableColumns(t, db, table)
		present := map[string]bool{}
		for _, c := range cols {
			present[c.name] = true
		}
		for i, row := range rows {
			for _, c := range cols {
				if _, ok := row[c.name]; !ok && c.notNull && !c.hasDflt && !c.pk {
					t.Fatalf("schema %d: %s.%s is NOT NULL without a default and sampleRows[%q][%d] has no value for it", version, table, c.name, table, i)
				}
			}
			names := make([]string, 0, len(row))
			for name := range row {
				if present[name] {
					names = append(names, name)
				}
			}
			sort.Strings(names)
			args := make([]any, len(names))
			marks := make([]string, len(names))
			for j, name := range names {
				args[j] = row[name]
				marks[j] = "?"
			}
			// Table and column names come from sqlite_master and the fixed
			// sampleRows literal above; every value is bound.
			q := `INSERT INTO "` + table + `" ("` + strings.Join(names, `","`) + `") VALUES (` + strings.Join(marks, ",") + `)` // #nosec G202 -- test-only, names from schema
			switch table {
			case "sync_state":
				q += ` ON CONFLICT (key) DO UPDATE SET value = excluded.value`
			case "migration_checksums":
				// buildAtVersion populates this table via migrate(); use upsert
				// so sampleRows can still exercise it without a unique conflict.
				q += ` ON CONFLICT (version) DO UPDATE SET name=excluded.name, checksum=excluded.checksum`
			}
			if _, err := db.Exec(q, args...); err != nil {
				t.Fatalf("schema %d: seed %s row %d: %v", version, table, i, err)
			}
		}
	}
}

// snapshot renders every row of every table as text, keyed by table, using
// only the given columns per table, sorted for comparison.
func snapshot(t *testing.T, db *sql.DB, columns map[string][]string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for table, cols := range columns {
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = `quote("` + c + `")`
		}
		rows, err := db.Query(`SELECT ` + strings.Join(quoted, ` || '|' || `) + ` FROM "` + table + `"`) // #nosec G202 -- test-only, names from schema
		if err != nil {
			t.Fatalf("snapshot %s: %v", table, err)
		}
		var lines []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			// The upgrade's one intended change to existing data.
			if table == "sync_state" && strings.HasPrefix(line, "'"+KeySchemaVersion+"'|") {
				continue
			}
			lines = append(lines, line)
		}
		_ = rows.Close()
		sort.Strings(lines)
		out[table] = lines
	}
	return out
}

func schemaColumns(t *testing.T, db *sql.DB) map[string][]column {
	t.Helper()
	out := map[string][]column{}
	for _, table := range userTables(t, db) {
		out[table] = tableColumns(t, db, table)
	}
	return out
}

func schemaSQL(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT type || ' ' || name || ': ' || COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// TestMigrationUpgradePath builds a database at every historical schema
// version N-1, fills it with representative rows, and upgrades it with the
// production Open path. It covers new migrations without edits: the loop
// runs over whatever migrations/ embeds.
func TestMigrationUpgradePath(t *testing.T) {
	ctx := context.Background()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 2 {
		t.Fatalf("expected at least two migrations, found %d", len(ms))
	}
	fresh, _ := openTest(t)
	wantSchema := schemaSQL(t, fresh.DB)

	for n := 2; n <= len(ms); n++ {
		t.Run(fmt.Sprintf("schema %d with data upgrades through %s", n-1, ms[n-1].name), func(t *testing.T) {
			p, db := buildAtVersion(t, ms, n-1)
			seed(t, db, n-1)
			before := schemaColumns(t, db)
			oldCols := map[string][]string{}
			for table, cols := range before {
				for _, c := range cols {
					oldCols[table] = append(oldCols[table], c.name)
				}
			}
			wantRows := snapshot(t, db, oldCols)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			s, err := Open(ctx, p, Options{Passphrase: testnet})
			if err != nil {
				t.Fatalf("upgrade from schema %d: %v", n-1, err)
			}
			defer func() { _ = s.Close() }()

			if v, err := s.State(ctx, KeySchemaVersion); err != nil || v != strconv.Itoa(len(ms)) {
				t.Fatalf("schema_version = %q, %v; want %d", v, err, len(ms))
			}
			if got := schemaSQL(t, s.DB); strings.Join(got, "\n") != strings.Join(wantSchema, "\n") {
				t.Errorf("upgraded schema differs from a fresh database:\n got: %s\nwant: %s", strings.Join(got, "\n"), strings.Join(wantSchema, "\n"))
			}

			// Every row present before the upgrade survives with the same
			// values in every column it had.
			gotRows := snapshot(t, s.DB, oldCols)
			for table, want := range wantRows {
				if strings.Join(gotRows[table], "\n") != strings.Join(want, "\n") {
					t.Errorf("%s rows changed by the upgrade:\n got: %v\nwant: %v", table, gotRows[table], want)
				}
				if len(want) == 0 {
					t.Errorf("%s had no sample rows at schema %d: the upgrade was not exercised with data", table, n-1)
				}
			}

			// Columns added after N-1 hold their declared default (or NULL)
			// on old rows: an upgrade never invents data for old rows.
			after := schemaColumns(t, s.DB)
			for table, cols := range after {
				had := map[string]bool{}
				for _, c := range before[table] {
					had[c.name] = true
				}
				for _, c := range cols {
					if had[c.name] || len(before[table]) == 0 {
						continue
					}
					want := "NULL"
					if c.hasDflt {
						if err := s.DB.QueryRow(`SELECT quote(` + c.dfltText + `)`).Scan(&want); err != nil { // #nosec G202 -- default text from the schema
							t.Fatal(err)
						}
					}
					var bad int
					if err := s.DB.QueryRow(`SELECT count(*) FROM "`+table+`" WHERE quote("`+c.name+`") <> ?`, want).Scan(&bad); err != nil { // #nosec G202 -- names from the schema
						t.Fatal(err)
					}
					if bad != 0 {
						t.Errorf("new column %s.%s: %d old rows are not %s", table, c.name, bad, want)
					}
				}
			}

			assertInvariantsHold(t, s.DB)
		})
	}
}

// TestMigration0002LeavesOldFunctionsNull pins the first real upgrade case
// (CLAUDE.md §5.8 amendment): Wasm analyzed before 0002 has no stored
// signatures, and --recompute must see NULL, not an empty list.
func TestMigration0002LeavesOldFunctionsNull(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	p, db := buildAtVersion(t, ms, 1)
	seed(t, db, 1)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), p, Options{Passphrase: testnet})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var total, null int
	if err := s.DB.QueryRow(`SELECT count(*), count(*) - count(functions_json) FROM wasm`).Scan(&total, &null); err != nil {
		t.Fatal(err)
	}
	if total != len(sampleRows["wasm"]) || null != total {
		t.Fatalf("after 0002: %d wasm rows, %d with NULL functions_json; want all %d NULL", total, null, len(sampleRows["wasm"]))
	}
	var withSpec int
	if err := s.DB.QueryRow(`SELECT count(*) FROM wasm WHERE has_spec = 1 AND functions_json IS NULL`).Scan(&withSpec); err != nil || withSpec == 0 {
		t.Fatalf("expected pre-0002 Wasm with a spec and no signatures (the --recompute re-fetch case); got %d, %v", withSpec, err)
	}
}

func init() {
	// Populate migration_checksums sampleRows with the real checksums so that
	// verifyChecksums() does not reject a database seeded for upgrade tests.
	ms, err := loadMigrations()
	if err != nil {
		panic("migration_upgrade_test init: " + err.Error())
	}
	rows := make([]map[string]any, 0, len(ms))
	for _, m := range ms {
		rows = append(rows, map[string]any{"version": m.version, "name": m.name, "checksum": m.checksum})
	}
	sampleRows["migration_checksums"] = rows
}
