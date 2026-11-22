-- 0006_provenance: records the transaction hash and source account that
-- created each contract version. Applied in one transaction by internal/store.
--
-- Both columns are NULL for rows written before this migration (seeded
-- contracts and contracts indexed before the upgrade) and for entries
-- created by ledger upgrades (which are not attributed to any transaction).

ALTER TABLE contract_versions ADD COLUMN created_tx_hash      TEXT;
ALTER TABLE contract_versions ADD COLUMN created_source_account TEXT;
