package store

import (
	"context"
	"database/sql"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// Statistics exposes adoption and gap counts from the same database snapshot.
// Anomalies count stored tokens across all indexed code, including old versions.
type Statistics struct {
	Network           string         `json:"network"`
	Adoption          Totals         `json:"adoption"`
	Anomalies         map[string]int `json:"anomalies"`
	ArchivedContracts int            `json:"archived_contracts"`
	TotalContracts    int            `json:"total_contracts"`
	LastLedger        uint32         `json:"last_ledger"`
	RulesetVersions   map[int]string `json:"ruleset_versions"`
	ParserVersion     string         `json:"parser_version"`
}

// Stats reads adoption, anomalies, archival and last sync without RPC or writes.
func (s *Store) Stats(ctx context.Context, network string, rulesets map[int]string) (Statistics, error) {
	d := Statistics{Network: network, Anomalies: map[string]int{}, RulesetVersions: rulesets, ParserVersion: sepmeta.ParserVersion}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return d, err
	}
	defer func() { _ = tx.Rollback() }()
	d.LastLedger, err = readLedger(ctx, tx)
	if err != nil {
		return d, err
	}
	d.Adoption, err = totals(ctx, tx, rulesets[41], false)
	if err != nil {
		return d, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(sum(archived),0),count(*) FROM contracts`).Scan(&d.ArchivedContracts, &d.TotalContracts); err != nil {
		return d, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT anomaly,count(*) FROM (SELECT anomaly FROM parse_anomalies UNION ALL SELECT anomaly FROM wasm_claims WHERE anomaly IS NOT NULL) GROUP BY anomaly ORDER BY anomaly`)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var a string
		var n int
		if err = rows.Scan(&a, &n); err != nil {
			break
		}
		d.Anomalies[a] = n
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return d, err
	}
	return d, tx.Commit()
}
