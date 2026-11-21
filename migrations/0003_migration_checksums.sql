-- 0003_migration_checksums: records a SHA-256 checksum for each applied
-- migration so startup can detect an edited migration file. Applied in one
-- transaction by internal/store.
--
-- existing_checksums is populated by internal/store when upgrading a
-- database that already has migrations applied but no checksum table yet.

CREATE TABLE migration_checksums (
  version  INTEGER PRIMARY KEY,
  name     TEXT NOT NULL,
  checksum TEXT NOT NULL           -- lowercase hex SHA-256 of the migration SQL
);
