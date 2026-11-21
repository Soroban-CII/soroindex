package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	sqlite "modernc.org/sqlite"
)

// Maintenance errors.
var (
	// ErrWriterActive means another connection holds the write lock, most
	// likely a running sync. Maintenance never waits for it or competes with
	// it: the operator stops the writer and runs maintenance again.
	ErrWriterActive = errors.New("another process holds the database write lock (stop sync before running maintenance)")
	// ErrIntegrity means PRAGMA integrity_check reported problems. Maintenance
	// then changes nothing: statistics and vacuum are skipped so the damaged
	// file stays as found for diagnosis or restore from backup.
	ErrIntegrity = errors.New("integrity check failed")
)

// MaintenanceOptions configure Maintain.
type MaintenanceOptions struct {
	// Passphrase is the configured network; a database for another network
	// is refused, as Open refuses it.
	Passphrase string
	// EnableIncrementalVacuum switches a database whose auto_vacuum mode is
	// not incremental to auto_vacuum=INCREMENTAL. SQLite applies that change
	// only through a full VACUUM, which rewrites the file and needs free disk
	// space about the size of the database, so it is never done implicitly.
	EnableIncrementalVacuum bool
}

// DatabaseSize is the database size from SQLite's own page counts and from
// the files on disk (the main file plus its WAL).
type DatabaseSize struct {
	PageSize      int64 `json:"page_size"`
	PageCount     int64 `json:"page_count"`
	FreelistCount int64 `json:"freelist_count"`
	Bytes         int64 `json:"bytes"`      // page_count * page_size
	FileBytes     int64 `json:"file_bytes"` // main file + -wal file on disk
}

// MaintenanceStep records one step and how long it took.
type MaintenanceStep struct {
	Name       string `json:"name"`
	Result     string `json:"result"`
	DurationMS int64  `json:"duration_ms"`
}

// MaintenanceReport is what Maintain did, for the CLI to print. Every step
// that ran is listed with its result; a skipped step says why.
type MaintenanceReport struct {
	Database    string            `json:"database"`
	IntegrityOK bool              `json:"integrity_ok"`
	Integrity   []string          `json:"integrity"`
	AutoVacuum  string            `json:"auto_vacuum"`
	FreedPages  int64             `json:"freed_pages"`
	Steps       []MaintenanceStep `json:"steps"`
	Before      DatabaseSize      `json:"size_before"`
	// After is zero when maintenance stopped before changing anything.
	After      DatabaseSize `json:"size_after"`
	DurationMS int64        `json:"duration_ms"`
}

var autoVacuumModes = map[int64]string{0: "none", 1: "full", 2: "incremental"}

// maintenanceIntegrityLimit caps the problems integrity_check lists.
const maintenanceIntegrityLimit = 100

// Maintain runs PRAGMA integrity_check, ANALYZE and an incremental vacuum on
// the database at path while holding SQLite's write lock, so no sync can
// write during the checks. It refuses, without waiting, if another
// connection already holds the lock. It never creates or migrates a
// database. Readers (the API) may stay open throughout.
func Maintain(ctx context.Context, path string, o MaintenanceOptions) (r MaintenanceReport, err error) {
	start := time.Now()
	r = MaintenanceReport{Database: path, Integrity: []string{}, Steps: []MaintenanceStep{}}
	defer func() { r.DurationMS = time.Since(start).Milliseconds() }()
	if o.Passphrase == "" {
		return r, errors.New("maintenance: network passphrase is required")
	}
	if _, err := os.Stat(path); err != nil {
		return r, fmt.Errorf("maintenance: %w", err)
	}
	q := url.Values{}
	q.Set("mode", "rw") // never create a missing file
	q.Add("_pragma", "busy_timeout(0)")
	q.Add("_pragma", "foreign_keys(1)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return r, fmt.Errorf("maintenance: open %s: %w", path, err)
	}
	defer func() { _ = db.Close() }() // read-write handle; every change is committed or rolled back below
	db.SetMaxOpenConns(1)
	if err := checkMaintainable(ctx, db, o.Passphrase); err != nil {
		return r, lockErr(err)
	}
	// Every later statement runs on this one connection, so the transaction
	// below and the pragmas after it share the lock and settings.
	conn, err := db.Conn(ctx)
	if err != nil {
		return r, fmt.Errorf("maintenance: open %s: %w", path, lockErr(err))
	}
	defer func() { _ = conn.Close() }() // returns the pinned connection to the pool
	if r.Before, err = databaseSize(ctx, conn, path); err != nil {
		return r, lockErr(err)
	}

	step := func(name string, f func() (string, error)) error {
		t := time.Now()
		res, err := f()
		if err != nil {
			res = "error: " + err.Error()
		}
		r.Steps = append(r.Steps, MaintenanceStep{Name: name, Result: res, DurationMS: time.Since(t).Milliseconds()})
		return err
	}

	if err := step("lock", func() (string, error) {
		if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
			return "", lockErr(err)
		}
		return "write lock held", nil
	}); err != nil {
		return r, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`) // release the lock; nothing was written
		}
	}()

	if err := step("integrity_check", func() (string, error) {
		lines, checkErr := integrityCheck(ctx, conn)
		r.Integrity = lines
		if checkErr != nil {
			// A malformed file can make the check itself fail; that is a
			// failed check, not a reason to continue.
			r.Integrity = append(r.Integrity, checkErr.Error())
		}
		r.IntegrityOK = checkErr == nil && len(r.Integrity) == 1 && r.Integrity[0] == "ok"
		if !r.IntegrityOK {
			return "", fmt.Errorf("%w: %d problem(s)", ErrIntegrity, len(r.Integrity))
		}
		return "ok", nil
	}); err != nil {
		return r, err
	}

	if err := step("analyze", func() (string, error) {
		if _, err := conn.ExecContext(ctx, `ANALYZE`); err != nil {
			return "", err
		}
		return "query statistics refreshed", nil
	}); err != nil {
		return r, err
	}

	var mode int64
	if err := conn.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return r, fmt.Errorf("maintenance: auto_vacuum: %w", err)
	}
	r.AutoVacuum = autoVacuumModes[mode]
	if mode == 2 {
		if err := step("incremental_vacuum", func() (string, error) {
			before := r.Before.FreelistCount
			if _, err := conn.ExecContext(ctx, `PRAGMA incremental_vacuum`); err != nil {
				return "", err
			}
			var after int64
			if err := conn.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&after); err != nil {
				return "", err
			}
			r.FreedPages = before - after
			return fmt.Sprintf("freed %d page(s)", r.FreedPages), nil
		}); err != nil {
			return r, err
		}
	}

	if err := step("commit", func() (string, error) {
		if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
			return "", lockErr(err)
		}
		committed = true
		return "write lock released", nil
	}); err != nil {
		return r, err
	}

	if mode != 2 {
		if !o.EnableIncrementalVacuum {
			r.Steps = append(r.Steps, MaintenanceStep{Name: "incremental_vacuum", Result: fmt.Sprintf(
				"skipped: auto_vacuum is %s, so SQLite cannot vacuum incrementally (%d free page(s)); "+
					"run once with --enable-incremental-vacuum, which does a full VACUUM", r.AutoVacuum, r.Before.FreelistCount)})
		} else if err := step("enable_incremental_vacuum", func() (string, error) {
			// VACUUM cannot run inside a transaction. It takes the write lock
			// itself and fails at once (busy_timeout 0) if a writer started
			// after the commit above.
			if _, err := conn.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
				return "", err
			}
			if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
				return "", lockErr(err)
			}
			if err := conn.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
				return "", err
			}
			r.AutoVacuum = autoVacuumModes[mode]
			r.FreedPages = r.Before.FreelistCount
			return fmt.Sprintf("full VACUUM done; auto_vacuum is now %s", r.AutoVacuum), nil
		}); err != nil {
			return r, err
		}
	}

	if err := step("wal_checkpoint", func() (string, error) {
		var busy, logPages, checkpointed int64
		if err := conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logPages, &checkpointed); err != nil {
			return "", err
		}
		if busy != 0 {
			return "partial: a reader kept the WAL open; it is truncated by a later checkpoint", nil
		}
		return "WAL checkpointed and truncated", nil
	}); err != nil {
		return r, err
	}
	if r.After, err = databaseSize(ctx, conn, path); err != nil {
		return r, err
	}
	return r, nil
}

// checkMaintainable applies Open's refusals without migrating: the file must
// hold this binary's schema version and the configured network.
func checkMaintainable(ctx context.Context, db *sql.DB, passphrase string) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	s := &Store{DB: db}
	current, err := s.schemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("maintenance: %w", err)
	}
	switch {
	case current == 0:
		return ErrReadOnlyUninitialized
	case current > len(ms):
		return fmt.Errorf("%w: database is at %d, binary knows %d", ErrSchemaTooNew, current, len(ms))
	case current < len(ms):
		return fmt.Errorf("%w: database is at %d, binary knows %d", ErrSchemaTooOld, current, len(ms))
	}
	stored, err := s.State(ctx, KeyNetworkPassphrase)
	if err != nil {
		return fmt.Errorf("maintenance: %w", err)
	}
	if stored != passphrase {
		return fmt.Errorf("%w: database is for %q, configured network is %q", ErrNetworkMismatch, stored, passphrase)
	}
	return nil
}

func integrityCheck(ctx context.Context, conn *sql.Conn) ([]string, error) {
	rows, err := conn.QueryContext(ctx, fmt.Sprintf(`PRAGMA integrity_check(%d)`, maintenanceIntegrityLimit))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // read-only cursor
	out := []string{}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return out, err
		}
		out = append(out, line)
	}
	return out, rows.Err()
}

func databaseSize(ctx context.Context, conn *sql.Conn, path string) (DatabaseSize, error) {
	var d DatabaseSize
	for _, p := range []struct {
		q   string
		dst *int64
	}{{`PRAGMA page_size`, &d.PageSize}, {`PRAGMA page_count`, &d.PageCount}, {`PRAGMA freelist_count`, &d.FreelistCount}} {
		if err := conn.QueryRowContext(ctx, p.q).Scan(p.dst); err != nil {
			return d, fmt.Errorf("maintenance: %s: %w", p.q, err)
		}
	}
	d.Bytes = d.PageSize * d.PageCount
	for _, f := range []string{path, path + "-wal"} {
		st, err := os.Stat(f)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return d, fmt.Errorf("maintenance: %w", err)
		}
		d.FileBytes += st.Size()
	}
	return d, nil
}

// lockErr maps SQLite's busy and locked results to ErrWriterActive.
func lockErr(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case 5, 6: // SQLITE_BUSY, SQLITE_LOCKED
			return fmt.Errorf("%w: %v", ErrWriterActive, err)
		}
	}
	return err
}
