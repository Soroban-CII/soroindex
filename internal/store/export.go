package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ExportTable names one CSV file in a full export. The set is fixed and its
// columns are a documented interface: researchers build on them, so adding a
// column is compatibility work, not an implementation detail.
type ExportTable string

// The export bundle, in the order the command writes it.
const (
	ExportContracts ExportTable = "contracts"
	ExportVersions  ExportTable = "versions"
	ExportWasm      ExportTable = "wasm"
	ExportClaims    ExportTable = "claims"
	ExportAnomalies ExportTable = "anomalies"
	ExportMatches   ExportTable = "matches"
)

// ExportTables lists the bundle in a stable order.
var ExportTables = []ExportTable{ExportContracts, ExportVersions, ExportWasm, ExportClaims, ExportAnomalies, ExportMatches}

// Columns returns the stable CSV header for one export table. A NULL column
// is written as an empty field; the doc comment on each table says which
// columns can be NULL.
func (t ExportTable) Columns() []string {
	switch t {
	case ExportContracts:
		// Tier columns are separate on purpose (CLAUDE.md §5.6): a contract
		// that matches SEP-41 without declaring it never appears under
		// declared_seps. protocol_seps is "41" only for a SAC.
		return []string{"contract_id", "kind", "current_wasm_hash", "exec_ref_owner", "exec_ref_tag", "sac_asset",
			"created_ledger", "updated_ledger", "archived", "declared_seps", "inferred_seps", "verified_seps", "protocol_seps"}
	case ExportVersions:
		return []string{"contract_id", "wasm_hash", "exec_ref_owner", "exec_ref_tag", "from_ledger", "to_ledger"}
	case ExportWasm:
		return []string{"hash", "size_bytes", "first_seen_ledger", "has_meta", "has_spec", "sep_entry_count", "meta_json",
			"functions_json", "interface_version", "interface_prerelease", "parse_status", "parse_error", "parser_version"}
	case ExportClaims:
		return []string{"wasm_hash", "sep", "source", "raw_token", "anomaly"}
	case ExportAnomalies:
		return []string{"wasm_hash", "raw_token", "anomaly"}
	case ExportMatches:
		return []string{"wasm_hash", "sep", "ruleset_version", "rule_file_sha256", "status", "ok_count",
			"missing_json", "mismatched_json", "matched_variants_json", "computed_at"}
	}
	return nil
}

// Snapshot is one read-only transaction over the index. Every query run
// through it sees the same committed state, so an export cannot mix rows
// from before and after a sync batch.
type Snapshot struct {
	tx       *sql.Tx
	versions map[int]string
}

// Snapshot runs fn against one read-only transaction and commits it. The
// database handle must have been opened read-only, as the API's is; this is
// the same guarantee the HTTP handlers rely on. versions maps SEP number to
// the ruleset version the binary loaded, so exported inferred tiers match
// what the API reports rather than every historical match.
func (s *Store) Snapshot(ctx context.Context, versions map[int]string, fn func(context.Context, *Snapshot) error) error {
	if !s.readOnly {
		return fmt.Errorf("export: snapshot requires a read-only store")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("export: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	sn := &Snapshot{tx: tx, versions: versions}
	if err := fn(ctx, sn); err != nil {
		return err
	}
	return tx.Commit()
}

// State reads one sync_state value inside the snapshot. The export manifest
// must record the ledger the data actually came from, not a later value read
// outside the transaction.
func (sn *Snapshot) State(ctx context.Context, key string) (string, error) {
	var v string
	err := sn.tx.QueryRowContext(ctx, `SELECT value FROM sync_state WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("sync_state %s: %w", key, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("sync_state %s: %w", key, err)
	}
	return v, nil
}

// Read returns the rows of one export table, ordered by its primary key so
// the same database always produces the same bytes. The caller must close
// the rows before running another query on the snapshot.
func (sn *Snapshot) Read(ctx context.Context, t ExportTable) (*sql.Rows, error) {
	q, args := exportQuery(t, sn.versions)
	if q == "" {
		return nil, fmt.Errorf("export: unknown table %q", t)
	}
	rows, err := sn.tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("export %s: %w", t, err)
	}
	return rows, nil
}

// exportQuery returns one table's streaming query. Every query is ordered,
// so a re-export of an unchanged index is byte-identical.
func exportQuery(t ExportTable, versions map[int]string) (string, []any) {
	switch t {
	case ExportContracts:
		versionsJSON := versionsJSON(versions)
		return `
SELECT c.contract_id,
       c.kind,
       COALESCE(c.current_wasm_hash, ''),
       COALESCE(c.exec_ref_owner, ''),
       COALESCE(c.exec_ref_tag, ''),
       COALESCE(c.sac_asset, ''),
       COALESCE(c.created_ledger, ''),
       COALESCE(c.updated_ledger, ''),
       c.archived,
       COALESCE((SELECT group_concat(sep, ',') FROM
         (SELECT DISTINCT sep FROM wasm_claims wc WHERE wc.wasm_hash = c.current_wasm_hash ORDER BY sep)), ''),
       COALESCE((SELECT group_concat(sep, ',') FROM
         (SELECT DISTINCT im.sep FROM interface_matches im
          WHERE im.wasm_hash = c.current_wasm_hash AND im.status = 'match'
            AND im.ruleset_version = json_extract(?, '$.' || im.sep) ORDER BY im.sep)), ''),
       COALESCE((SELECT group_concat(sep, ',') FROM
         (SELECT DISTINCT v.sep FROM verifications v
          JOIN wasm_claims wc ON wc.wasm_hash = v.wasm_hash AND wc.sep = v.sep
          WHERE v.contract_id = c.contract_id AND v.wasm_hash = c.current_wasm_hash
            AND v.passed = 1 AND v.verdict = 'pass' ORDER BY v.sep)), ''),
       CASE WHEN c.kind = 'sac' THEN '41' ELSE '' END
FROM contracts c
ORDER BY c.contract_id`, []any{versionsJSON}
	case ExportVersions:
		return `
SELECT contract_id, COALESCE(wasm_hash, ''), COALESCE(exec_ref_owner, ''), COALESCE(exec_ref_tag, ''),
       from_ledger, COALESCE(to_ledger, '')
FROM contract_versions
ORDER BY contract_id, from_ledger`, nil
	case ExportWasm:
		return `
SELECT hash, COALESCE(size_bytes, ''), COALESCE(first_seen_ledger, ''), has_meta, has_spec, sep_entry_count,
       COALESCE(meta_json, ''), COALESCE(functions_json, ''), COALESCE(interface_version, ''),
       COALESCE(interface_prerelease, ''), parse_status, COALESCE(parse_error, ''), parser_version
FROM wasm
ORDER BY hash`, nil
	case ExportClaims:
		return `
SELECT wasm_hash, sep, source, raw_token, COALESCE(anomaly, '')
FROM wasm_claims
ORDER BY wasm_hash, sep, source`, nil
	case ExportAnomalies:
		return `
SELECT wasm_hash, raw_token, anomaly
FROM parse_anomalies
ORDER BY wasm_hash, raw_token`, nil
	case ExportMatches:
		return `
SELECT wasm_hash, sep, ruleset_version, rule_file_sha256, status, ok_count,
       missing_json, mismatched_json, matched_variants_json, computed_at
FROM interface_matches
ORDER BY wasm_hash, sep, ruleset_version`, nil
	}
	return "", nil
}

// versionsJSON encodes the loaded ruleset map for SQLite's json_extract. It
// cannot fail: both key and value are plain strings and ints.
func versionsJSON(versions map[int]string) string {
	if len(versions) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteByte('{')
	first := true
	for sep, version := range versions {
		if !first {
			b.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&b, "%q:%q", fmt.Sprint(sep), version)
	}
	b.WriteByte('}')
	return b.String()
}
