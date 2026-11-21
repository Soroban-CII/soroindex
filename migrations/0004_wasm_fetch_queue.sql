-- 0004_wasm_fetch_queue: retry queue for Wasm fetches that failed after
-- RPC retries exhausted. This prevents a single unreachable entry from
-- stalling the entire sync, while ensuring it is not silently skipped.
-- Approved by operator on 2026-11-21 for issue #90.

CREATE TABLE wasm_fetch_queue (
  hash            TEXT PRIMARY KEY,          -- hex
  first_failed_at TEXT NOT NULL,             -- RFC3339 of first failure
  last_attempt_at TEXT,                      -- RFC3339 of last retry attempt, NULL if never retried
  attempt_count   INTEGER NOT NULL DEFAULT 1,
  next_retry_at   TEXT NOT NULL,             -- RFC3339 when next retry is due
  last_error      TEXT NOT NULL              -- error message from last attempt
);

CREATE INDEX idx_wasm_fetch_queue_next_retry ON wasm_fetch_queue (next_retry_at);