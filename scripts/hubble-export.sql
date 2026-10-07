-- Hubble export for Phase 0 and seeding (CLAUDE.md §5.10).
--
-- Table and column names were confirmed on 2026-10-07 against the Hubble
-- data dictionary on developers.stellar.org, and the string values against
-- stellar-etl's transform code (stellar/stellar-etl@34f6910b81,
-- internal/transform/contract_data.go: contract_key_type is
-- Key.Type.String() and contract_durability is Durability.String() from the
-- Go SDK).
--
-- The four queries are separate. Run each one on its own and save its CSV
-- under the name given in its header. Every query prunes columns and filters
-- on the table's partition column, because these tables are billed per byte
-- scanned. Check the cost first with --dry_run:
--
--   bq query --nouse_legacy_sql --dry_run "$(sed -n '/^-- Q1/,/^-- END Q1/p' scripts/hubble-export.sql)"
--
-- Then export, for example:
--
--   bq query --nouse_legacy_sql --format=csv --max_rows=100000000 \
--     "$(sed -n '/^-- Q1/,/^-- END Q1/p' scripts/hubble-export.sql)" > report/mainnet_hashes.csv
--
-- Replace Q1 with Q0, Q2 or Q3 for the other queries. Hubble covers
-- mainnet only; testnet Phase 0 samples getLedgers instead.


-- Q0  Sanity check: which key types and durabilities exist in current
--     contract data. Output: report/mainnet_key_types.csv
--     Run this first. Q2 and Q3 filter on the exact strings
--     'ScValTypeScvLedgerKeyContractInstance' and 'ScValTypeScvExecutableTag'.
--     If Q0 shows the instance count under a different spelling, stop and
--     report it rather than editing Q2.
SELECT
  contract_key_type,
  contract_durability,
  deleted,
  COUNT(*) AS n
FROM `crypto-stellar.snapshots.contract_data_snapshot`
WHERE valid_to IS NULL                 -- current rows only; prunes to one partition
GROUP BY contract_key_type, contract_durability, deleted
ORDER BY n DESC;
-- END Q0


-- Q1  Every contract code hash with its latest state.
--     Output: report/mainnet_hashes.csv
--     Columns: contract_code_hash, deleted, last_modified_ledger, closed_at
--     The bronze table holds every change, so keep the latest row per hash.
--     Rows with deleted = true are kept and counted separately by phase0, and
--     its census uses only deleted = false.
--     Partition bound: closed_at from 2024-02-01, before Soroban's mainnet
--     activation (protocol 20, February 2024), so no code entry is excluded.
SELECT
  contract_code_hash,
  deleted,
  last_modified_ledger,
  closed_at
FROM `crypto-stellar.crypto_stellar.contract_code`
WHERE closed_at >= TIMESTAMP('2024-02-01')
QUALIFY ROW_NUMBER() OVER (
  PARTITION BY contract_code_hash
  ORDER BY last_modified_ledger DESC, ledger_sequence DESC
) = 1
ORDER BY contract_code_hash;
-- END Q1


-- Q2  Every current contract instance.
--     Output: report/mainnet_instances.csv
--     Columns: contract_id, contract_data_xdr, asset_code, asset_issuer,
--              asset_type, last_modified_ledger
--     contract_data_xdr is the base64 ContractDataEntry; phase0 decodes its
--     executable (Wasm hash, Stellar Asset, or CAP-85 external reference)
--     locally, so no RPC call per contract is needed.
SELECT
  contract_id,
  contract_data_xdr,
  asset_code,
  asset_issuer,
  asset_type,
  last_modified_ledger
FROM `crypto-stellar.snapshots.contract_data_snapshot`
WHERE valid_to IS NULL
  AND contract_key_type = 'ScValTypeScvLedgerKeyContractInstance'
  AND contract_durability = 'ContractDataDurabilityPersistent'
  AND deleted = FALSE
ORDER BY contract_id;
-- END Q2


-- Q3  Every current CAP-85 executable reference entry.
--     Output: report/mainnet_exec_refs.csv
--     Columns: contract_id (the owner), contract_data_xdr, last_modified_ledger
--     May return zero rows. That is expected if no references exist, and also
--     if Hubble's loader predates CAP-85. Q0 tells the two apart. phase0
--     resolves any reference it cannot find here with getLedgerEntries.
SELECT
  contract_id,
  contract_data_xdr,
  last_modified_ledger
FROM `crypto-stellar.snapshots.contract_data_snapshot`
WHERE valid_to IS NULL
  AND contract_key_type = 'ScValTypeScvExecutableTag'
  AND contract_durability = 'ContractDataDurabilityPersistent'
  AND deleted = FALSE
ORDER BY contract_id;
-- END Q3
