-- 0002_functions_json: keep each Wasm's function signatures so --recompute
-- can re-run the matcher for a new ruleset version without re-fetching code
-- (CLAUDE.md §5.8, §5.9; operator-approved 2026-10-07). NULL for Wasm with
-- no spec, and for Wasm analyzed before this migration.
ALTER TABLE wasm ADD COLUMN functions_json TEXT;
