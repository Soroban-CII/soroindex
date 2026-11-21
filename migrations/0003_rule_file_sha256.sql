-- 0003_rule_file_sha256: record the SHA-256 of the exact rule file used for
-- each interface_matches row. This proves which bytes produced a verdict, so a
-- changed file with an unchanged ruleset_version label cannot go unnoticed.
--
-- Operator-approved: 2026-10-07.
ALTER TABLE interface_matches ADD COLUMN rule_file_sha256 TEXT NOT NULL DEFAULT 'unknown';