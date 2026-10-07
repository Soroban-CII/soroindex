-- 0001_init: the schema from CLAUDE.md §5.8, including the CAP-85
-- amendment approved on 2026-10-07 (kind 'wasm_ref', exec_ref_* columns,
-- the exec_refs table). Applied in one transaction by internal/store.
--
-- Invariants live here, in SQL, so no code path can bypass them:
--   * one current version per contract (trigger trg_one_current_version);
--   * only a pass is passed (CHECK on verifications);
--   * a reference contract names its reference, and only it does (CHECKs).

CREATE TABLE contracts (
  contract_id       TEXT PRIMARY KEY,          -- C... strkey
  kind              TEXT NOT NULL CHECK (kind IN ('wasm','wasm_ref','sac')),
  current_wasm_hash TEXT,                      -- NULL for sac; for wasm_ref the resolved hash, NULL if unresolved
  exec_ref_owner    TEXT,                      -- C... owner; NOT NULL iff kind = 'wasm_ref'
  exec_ref_tag      TEXT,                      -- NOT NULL iff kind = 'wasm_ref'
  sac_asset         TEXT,                      -- 'native' | 'CODE:ISSUER', NULL for wasm
  created_ledger    INTEGER,
  updated_ledger    INTEGER,
  archived          INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0,1)),
  CHECK ((kind = 'wasm_ref') = (exec_ref_owner IS NOT NULL AND exec_ref_tag IS NOT NULL)),
  CHECK (kind = 'wasm_ref' OR (exec_ref_owner IS NULL AND exec_ref_tag IS NULL)),
  CHECK (kind <> 'sac' OR current_wasm_hash IS NULL),
  CHECK (kind = 'sac' OR sac_asset IS NULL)
);

CREATE TABLE wasm (
  hash              TEXT PRIMARY KEY,          -- hex
  size_bytes        INTEGER,
  first_seen_ledger INTEGER,
  has_meta          INTEGER NOT NULL DEFAULT 0 CHECK (has_meta IN (0,1)),
  has_spec          INTEGER NOT NULL DEFAULT 0 CHECK (has_spec IN (0,1)),
  sep_entry_count   INTEGER NOT NULL DEFAULT 0,
  meta_json         TEXT,                      -- all raw key/value pairs
  parse_status      TEXT NOT NULL CHECK (parse_status IN ('ok','partial','error','archived')),
  parse_error       TEXT,
  parser_version    TEXT NOT NULL
);

CREATE TABLE contract_versions (
  contract_id    TEXT NOT NULL,
  wasm_hash      TEXT,                         -- NULL for sac, or for an unresolved wasm_ref
  exec_ref_owner TEXT,                         -- set when this version's code came through an executable reference
  exec_ref_tag   TEXT,
  from_ledger    INTEGER NOT NULL,
  to_ledger      INTEGER,                      -- NULL while current
  PRIMARY KEY (contract_id, from_ledger),
  CHECK (to_ledger IS NULL OR to_ledger >= from_ledger),
  CHECK ((exec_ref_owner IS NULL) = (exec_ref_tag IS NULL))
);

CREATE TABLE exec_refs (                       -- CAP-85 executable reference entries
  owner_contract_id TEXT NOT NULL,
  tag               TEXT NOT NULL,
  wasm_hash         TEXT,                      -- NULL until resolved
  updated_ledger    INTEGER,
  archived          INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0,1)),
  PRIMARY KEY (owner_contract_id, tag)
);

CREATE TABLE wasm_claims (
  wasm_hash TEXT NOT NULL,
  sep       INTEGER NOT NULL,
  source    TEXT NOT NULL,
  raw_token TEXT NOT NULL,
  anomaly   TEXT,
  PRIMARY KEY (wasm_hash, sep, source)
);

CREATE TABLE parse_anomalies (                 -- tokens that produced no SEP
  wasm_hash TEXT NOT NULL,
  raw_token TEXT NOT NULL,
  anomaly   TEXT NOT NULL,
  PRIMARY KEY (wasm_hash, raw_token)
);

CREATE TABLE interface_matches (
  wasm_hash             TEXT NOT NULL,
  sep                   INTEGER NOT NULL,
  ruleset_version       TEXT NOT NULL,
  status                TEXT NOT NULL CHECK (status IN ('match','partial','mismatch','no_spec')),
  ok_count              INTEGER NOT NULL,
  missing_json          TEXT NOT NULL,
  mismatched_json       TEXT NOT NULL,
  matched_variants_json TEXT NOT NULL,
  computed_at           TEXT NOT NULL,
  PRIMARY KEY (wasm_hash, sep, ruleset_version)
);

CREATE TABLE verifications (
  contract_id  TEXT NOT NULL,
  wasm_hash    TEXT NOT NULL,
  sep          INTEGER NOT NULL,
  tool         TEXT NOT NULL,
  tool_version TEXT NOT NULL,
  verdict      TEXT NOT NULL CHECK (verdict IN ('pass','fail','unverifiable')),
  passed       INTEGER NOT NULL CHECK (passed IN (0,1)),  -- 1 only when verdict = 'pass'
  clauses_json TEXT NOT NULL,
  run_at       TEXT NOT NULL,
  PRIMARY KEY (contract_id, wasm_hash, sep, tool, run_at),
  CHECK (passed = 0 OR verdict = 'pass')
);

CREATE TABLE sync_state (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE INDEX idx_claims_sep       ON wasm_claims (sep, wasm_hash);
CREATE INDEX idx_contracts_hash   ON contracts (current_wasm_hash);
CREATE INDEX idx_contracts_ref    ON contracts (exec_ref_owner, exec_ref_tag) WHERE kind = 'wasm_ref';
CREATE INDEX idx_matches_sep      ON interface_matches (sep, status);
CREATE INDEX idx_versions_current ON contract_versions (contract_id) WHERE to_ledger IS NULL;

-- One current version per contract: reject a second open row, whether it
-- arrives by INSERT or by an UPDATE that reopens a closed row.
CREATE TRIGGER trg_one_current_version
BEFORE INSERT ON contract_versions
WHEN NEW.to_ledger IS NULL
 AND EXISTS (SELECT 1 FROM contract_versions
             WHERE contract_id = NEW.contract_id AND to_ledger IS NULL)
BEGIN
  SELECT RAISE(ABORT, 'contract_versions: contract already has a current version');
END;

CREATE TRIGGER trg_one_current_version_update
BEFORE UPDATE OF to_ledger, contract_id ON contract_versions
WHEN NEW.to_ledger IS NULL
 AND EXISTS (SELECT 1 FROM contract_versions
             WHERE contract_id = NEW.contract_id AND to_ledger IS NULL
               AND rowid <> OLD.rowid)
BEGIN
  SELECT RAISE(ABORT, 'contract_versions: contract already has a current version');
END;

CREATE VIEW claims AS
  SELECT v.contract_id, v.wasm_hash, c.sep, c.source, c.raw_token, c.anomaly,
         (v.to_ledger IS NULL) AS current
  FROM contract_versions v JOIN wasm_claims c ON c.wasm_hash = v.wasm_hash;
