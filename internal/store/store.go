// Package store is the SQLite database: schema migrations, the invariants
// they enforce, and the queries the indexer and API run. One database file
// holds one network (CLAUDE.md §5.8).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
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
	version int
	name    string
	sql     string
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
		out = append(out, migration{version: v, name: n, sql: string(b)})
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
	for _, m := range ms {
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sync_state (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
			KeySchemaVersion, strconv.Itoa(m.version)); err != nil {
			return fmt.Errorf("record migration %s: %w", m.name, err)
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

// Backup creates a consistent snapshot of the database at dst using SQLite's
// VACUUM INTO statement. VACUUM INTO uses an exclusive read transaction on the
// source, so it produces a point-in-time copy that is consistent with any
// committed writes; WAL-mode concurrent writes continue while it runs.
//
// dst must not already exist: SQLite refuses to overwrite an existing file.
// After the snapshot is written, Backup opens it read-only and validates the
// network passphrase, schema version, and committed ledger.
func (s *Store) Backup(ctx context.Context, dst string) error {
	if _, err := s.DB.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		// Remove a partial file so a re-run can try again from scratch.
		_ = os.Remove(dst)
		return fmt.Errorf("backup %s: %w", dst, err)
	}
	return s.validateSnapshot(ctx, dst)
}

// validateSnapshot opens dst read-only and checks that it carries the same
// network passphrase and schema version as the source, and that its own
// last_ledger value (if present) is a parseable uint32.
func (s *Store) validateSnapshot(ctx context.Context, dst string) error {
	q := url.Values{}
	q.Set("mode", "ro")
	snap, err := sql.Open("sqlite", "file:"+dst+"?"+q.Encode())
	if err != nil {
		return fmt.Errorf("backup validate: open %s: %w", dst, err)
	}
	defer func() { _ = snap.Close() }()
	b := &Store{DB: snap, readOnly: true}

	// Schema version must match.
	srcVer, err := s.schemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("backup validate: source schema: %w", err)
	}
	dstVer, err := b.schemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("backup validate: snapshot schema: %w", err)
	}
	if srcVer != dstVer {
		return fmt.Errorf("backup validate: schema version mismatch: source %d, snapshot %d", srcVer, dstVer)
	}

	// Network passphrase must match.
	srcPass, err := s.State(ctx, KeyNetworkPassphrase)
	if err != nil {
		return fmt.Errorf("backup validate: source passphrase: %w", err)
	}
	dstPass, err := b.State(ctx, KeyNetworkPassphrase)
	if err != nil {
		return fmt.Errorf("backup validate: snapshot passphrase: %w", err)
	}
	if srcPass != dstPass {
		return fmt.Errorf("%w: backup validate: passphrase mismatch: source %q, snapshot %q", ErrNetworkMismatch, srcPass, dstPass)
	}

	// If a committed ledger is recorded, it must be a valid uint32.
	if ledger, err := b.State(ctx, KeyLastLedger); err == nil {
		if _, err := strconv.ParseUint(ledger, 10, 32); err != nil {
			return fmt.Errorf("backup validate: last_ledger %q in snapshot is not a valid ledger number: %w", ledger, err)
		}
	} else if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("backup validate: last_ledger: %w", err)
	}

	return nil
}
