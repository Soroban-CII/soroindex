-- Migration 0005: the interface version a Wasm was built against.
--
-- Soroban code carries a contractenvmetav0 custom section with the
-- (protocol, pre-release) pair the toolchain targeted (SEP-46). It is
-- informational: it says which protocol the code expected, not what the
-- contract does. Both columns are nullable, and NULL means "no readable
-- contractenvmetav0 section", which is different from protocol 0.
ALTER TABLE wasm ADD COLUMN interface_version INTEGER;
ALTER TABLE wasm ADD COLUMN interface_prerelease INTEGER;
