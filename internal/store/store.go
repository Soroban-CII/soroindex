// Package store is the SQLite database: schema migrations, the invariants
// they enforce, and the queries the indexer and API run. One database file
// holds one network (CLAUDE.md §5.8).
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Soroban-CII/soroindex/migrations"
	_ "modernc.org/sqlite" // registers the CGO-free "sqlite" driver
)

// Sentinel errors.
var (
	// ErrSchemaTooNew means the database was migrated by a newer binary.
	ErrSchemaTooNew = errors.New("database schema is newer than this binary")
	// ErrNetworkMismatch means the database belongs to another network.
	ErrNetworkMismatch = errors.New("database network passphrase does not match")
	// ErrNotFound means a requested row does not exist.
	ErrNotFound = errors.New("not found")
	// ErrReadOnlyUninitialized means a read-only open found no schema.
	ErrReadOnlyUninitialized = errors.New("database has no schema; run sync first")
	// ErrSchemaTooOld means a read-only open found a schema older than the
	// binary; only a read-write open (sync) can migrate it.
	ErrSchemaTooOld = errors.New("database schema is older than this binary; run sync to migrate it")
	// ErrChecksumMismatch means a migration file on disk differs from what
	// was applied to the database; the file must not be edited after it ships.
	ErrChecksumMismatch = errors.New("migration checksum mismatch: file has been edited after it was applied")
)

// sync_state keys (CLAUDE.md §5.8).
const (
	KeyLastLedger        = "last_ledger"
	KeyNetworkPassphrase = "network_passphrase"
	KeySchemaVersion     = "schema_version"
	KeyRPCURL            = "rpc_url"
	KeyRulesetVersions   = "ruleset_versions"
)

// Store wraps the database handle.
type Store struct {
	DB       *sql.DB
	readOnly bool
}

// Options configure Open.
type Options struct {
	// Passphrase is the configured network. Open refuses a database that
	// records a different one, and records it in a new database.
	Passphrase string
	// ReadOnly opens with mode=ro and applies no migrations; the API uses it.
	ReadOnly bool
}

type migration struct {
	version  int
	name     string
	sql      string
	checksum string // lowercase hex SHA-256 of sql
}

// checksumSQL returns the lowercase hex SHA-256 of a migration SQL string.
func checksumSQL(sql string) string {
	h := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(h[:])
}

func loadMigrations() ([]migration, error) {
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, n := range names {
		num, _, ok := strings.Cut(path.Base(n), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("migration %q: name must be NNNN_name.sql", n)
		}
		b, err := fs.ReadFile(migrations.FS, n)
		if err != nil {
			return nil, err
		}
		s := string(b)
		out = append(out, migration{version: v, name: n, sql: s, checksum: checksumSQL(s)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migrations must be numbered 1..n without gaps; found %s at position %d", m.name, i+1)
		}
	}
	return out, nil
}

// SchemaVersion is the highest migration this binary knows.
func SchemaVersion() (int, error) {
	ms, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	return len(ms), nil
}

// Open opens (and for read-write, creates and migrates) the database at
// dbPath. It refuses to continue if the database was written by a newer
// binary or for another network.
func Open(ctx context.Context, dbPath string, o Options) (*Store, error) {
	if o.Passphrase == "" {
		return nil, errors.New("store: network passphrase is required")
	}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(10000)")
	if o.ReadOnly {
		q.Set("mode", "ro")
	} else {
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "synchronous(NORMAL)")
	}
	dsn := "file:" + dbPath + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	if !o.ReadOnly {
		// One writer: SQLite serializes writes anyway, and a single
		// connection keeps transactions and pragmas simple.
		db.SetMaxOpenConns(1)
	}
	s := &Store{DB: db, readOnly: o.ReadOnly}
	if err := s.init(ctx, o.Passphrase); err != nil {
		_ = db.Close() // the init error is the one worth reporting
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) init(ctx context.Context, passphrase string) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	current, err := s.schemaVersion(ctx)
	if err != nil {
		return err
	}
	if current > len(ms) {
		return fmt.Errorf("%w: database is at %d, binary knows %d", ErrSchemaTooNew, current, len(ms))
	}
	if s.readOnly {
		if current == 0 {
			return ErrReadOnlyUninitialized
		}
		if current < len(ms) {
			return fmt.Errorf("%w: database is at %d, binary knows %d", ErrSchemaTooOld, current, len(ms))
		}
	} else if current < len(ms) {
		if err := s.migrate(ctx, ms[current:]); err != nil {
			return err
		}
	}
	if !s.readOnly {
		if err := s.verifyChecksums(ctx, ms); err != nil {
			return err
		}
	}
	stored, err := s.State(ctx, KeyNetworkPassphrase)
	switch {
	case errors.Is(err, ErrNotFound):
		if s.readOnly {
			return fmt.Errorf("%w: database records no network", ErrNetworkMismatch)
		}
		return s.SetState(ctx, KeyNetworkPassphrase, passphrase)
	case err != nil:
		return err
	case stored != passphrase:
		return fmt.Errorf("%w: database is for %q, configured network is %q", ErrNetworkMismatch, stored, passphrase)
	}
	return nil
}

// schemaVersion reads sync_state.schema_version, or 0 for an empty file.
func (s *Store) schemaVersion(ctx context.Context) (int, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'sync_state'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("inspect schema: %w", err)
	}
	if n == 0 {
		return 0, nil
	}
	v, err := s.State(ctx, KeySchemaVersion)
	if errors.Is(err, ErrNotFound) {
		return 0, errors.New("sync_state exists but has no schema_version")
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

// migrate applies ms and records the new version in one transaction, so a
// failure leaves the database exactly as it was.
func (s *Store) migrate(ctx context.Context, ms []migration) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	// checksumTableExists tracks whether migration_checksums is available
	// within this transaction (it may be created mid-loop by migration 0005).
	checksumTableExists := false
	for _, m := range ms {
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sync_state (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
			KeySchemaVersion, strconv.Itoa(m.version)); err != nil {
			return fmt.Errorf("record migration %s: %w", m.name, err)
		}
		// Once migration_checksums exists (created by or before this migration),
		// record the checksum of every migration including the one that created it.
		if !checksumTableExists {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='migration_checksums'`).Scan(&n); err == nil && n > 0 {
				checksumTableExists = true
			}
		}
		if checksumTableExists {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO migration_checksums (version, name, checksum) VALUES (?, ?, ?) ON CONFLICT (version) DO UPDATE SET name=excluded.name, checksum=excluded.checksum`,
				m.version, m.name, m.checksum); err != nil {
				return fmt.Errorf("record checksum for %s: %w", m.name, err)
			}
		}
	}
	return tx.Commit()
}

// verifyChecksums compares stored checksums for applied migrations against
// the binary's embedded copies. It also backfills checksums for applied
// migrations that predate migration 0005 (the first run after upgrading).
func (s *Store) verifyChecksums(ctx context.Context, ms []migration) error {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='migration_checksums'`).Scan(&n); err != nil || n == 0 {
		return nil // table not yet created; nothing to check
	}
	// Build a map of stored checksums.
	rows, err := s.DB.QueryContext(ctx, `SELECT version, checksum FROM migration_checksums`)
	if err != nil {
		return fmt.Errorf("read migration checksums: %w", err)
	}
	stored := map[int]string{}
	for rows.Next() {
		var ver int
		var sum string
		if err := rows.Scan(&ver, &sum); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan migration checksum: %w", err)
		}
		stored[ver] = sum
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read migration checksums: %w", err)
	}
	_ = rows.Close()

	// Verify stored checksums; backfill any that are missing.
	needBackfill := false
	for _, m := range ms {
		if sum, ok := stored[m.version]; ok {
			if sum != m.checksum {
				return fmt.Errorf("%w: migration %s stored=%s binary=%s", ErrChecksumMismatch, m.name, sum, m.checksum)
			}
		} else {
			needBackfill = true
		}
	}
	if !needBackfill {
		return nil
	}
	// Backfill missing checksums in one transaction.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, m := range ms {
		if _, ok := stored[m.version]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO migration_checksums (version, name, checksum) VALUES (?, ?, ?) ON CONFLICT (version) DO NOTHING`,
			m.version, m.name, m.checksum); err != nil {
			return fmt.Errorf("backfill checksum for %s: %w", m.name, err)
		}
	}
	return tx.Commit()
}

// State reads one sync_state value.
func (s *Store) State(ctx context.Context, key string) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM sync_state WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("sync_state %s: %w", key, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("sync_state %s: %w", key, err)
	}
	return v, nil
}

// SetState writes one sync_state value.
func (s *Store) SetState(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO sync_state (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("set sync_state %s: %w", key, err)
	}
	return nil
}

// IsReadOnly lets HTTP servers reject handles capable of changing the index.
func (s *Store) IsReadOnly() bool { return s.readOnly }

// CountWasmFetchQueue returns the number of queued wasm fetches.
func (s *Store) CountWasmFetchQueue(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM wasm_fetch_queue`).Scan(&n)
	return n, err
}

// ListWasmFetchQueue returns all queued wasm fetches.
func (s *Store) ListWasmFetchQueue(ctx context.Context) ([]WasmFetchQueueRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT hash, first_failed_at, last_attempt_at, attempt_count, next_retry_at, last_error FROM wasm_fetch_queue ORDER BY next_retry_at`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []WasmFetchQueueRow
	for rows.Next() {
		var r WasmFetchQueueRow
		var lastAttempt sql.NullString
		if err := rows.Scan(&r.Hash, &r.FirstFailedAt, &lastAttempt, &r.AttemptCount, &r.NextRetryAt, &r.LastError); err != nil {
			return nil, err
		}
		if lastAttempt.Valid {
			r.LastAttemptAt = &lastAttempt.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MigrationRow describes one migration in a status or dry-run report.
type MigrationRow struct {
	Version  int    `json:"version"`
	Name     string `json:"name"`
	Applied  bool   `json:"applied"`
	Checksum string `json:"checksum"`
	Tampered bool   `json:"tampered,omitempty"` // stored checksum differs from binary
}

// MigrateStatusOptions configures MigrateStatus and MigrateDryRun.
type MigrateStatusOptions struct {
	Passphrase string
	ReadOnly   bool // open the database read-only (for status)
}

// MigrateStatus opens the database and returns applied/pending migration rows.
// It makes no changes.
func MigrateStatus(ctx context.Context, dbPath string, _ MigrateStatusOptions) ([]MigrationRow, error) {
	ms, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Set("mode", "ro")
	dsn := "file:" + dbPath + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }()

	var current int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='sync_state'`).Scan(&current); err != nil {
		return nil, err
	}
	appliedVer := 0
	if current > 0 {
		var v string
		err := db.QueryRowContext(ctx, `SELECT value FROM sync_state WHERE key = ?`, KeySchemaVersion).Scan(&v)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("read schema_version: %w", err)
		}
		if err == nil {
			if appliedVer, err = strconv.Atoi(v); err != nil {
				return nil, fmt.Errorf("parse schema_version: %w", err)
			}
		}
	}
	// Read stored checksums.
	stored := map[int]string{}
	var hasChecksums int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='migration_checksums'`).Scan(&hasChecksums)
	if hasChecksums > 0 {
		rows, err := db.QueryContext(ctx, `SELECT version, checksum FROM migration_checksums`)
		if err != nil {
			return nil, fmt.Errorf("read migration_checksums: %w", err)
		}
		for rows.Next() {
			var ver int
			var sum string
			if e := rows.Scan(&ver, &sum); e != nil {
				_ = rows.Close()
				return nil, e
			}
			stored[ver] = sum
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	out := make([]MigrationRow, len(ms))
	for i, m := range ms {
		r := MigrationRow{Version: m.version, Name: m.name, Checksum: m.checksum}
		if m.version <= appliedVer {
			r.Applied = true
			if storedSum, ok := stored[m.version]; ok && storedSum != m.checksum {
				r.Tampered = true
			}
		}
		out[i] = r
	}
	return out, nil
}

// MigrateDryRun returns pending migrations that would be applied, with no
// changes to the database.
func MigrateDryRun(ctx context.Context, dbPath string, o MigrateStatusOptions) ([]MigrationRow, error) {
	rows, err := MigrateStatus(ctx, dbPath, o)
	if err != nil {
		return nil, err
	}
	var pending []MigrationRow
	for _, r := range rows {
		if !r.Applied {
			pending = append(pending, r)
		}
	}
	return pending, nil
}
