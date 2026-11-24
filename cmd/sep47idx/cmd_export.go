package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

func init() {
	commands["export"] = command{summary: "write a checksummed CSV bundle of the index and a manifest", run: runExport}
}

// exportManifest is manifest.json. It deliberately carries no timestamp:
// two exports of an unchanged index must be byte-identical, and a clock
// would break that. The ledger and schema versions say exactly which data
// the files hold.
type exportManifest struct {
	Format          string               `json:"format"`
	FormatVersion   int                  `json:"format_version"`
	Network         string               `json:"network"`
	LastLedger      uint32               `json:"last_ledger"`
	SchemaVersion   int                  `json:"schema_version"`
	ParserVersion   string               `json:"parser_version"`
	RulesetVersions map[int]string       `json:"ruleset_versions"`
	Files           []exportManifestFile `json:"files"`
}

type exportManifestFile struct {
	Name   string `json:"name"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}

func runExport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	resolve := config.RegisterCommon(fs)
	out := fs.String("out", "", "directory to write the CSV bundle into (created if missing)")
	if err := fs.Parse(args); err != nil {
		return readExit("export", err, stderr)
	}
	cfg, err := resolve()
	if err != nil {
		return readExit("export", err, stderr)
	}
	if fs.NArg() != 0 {
		return readExit("export", fmt.Errorf("takes no positional arguments"), stderr)
	}
	if *out == "" {
		return readExit("export", errors.New("--out is required: it names the directory to write"), stderr)
	}
	versions, err := loadedRulesets()
	if err != nil {
		return readExit("export", err, stderr)
	}
	schema, err := store.SchemaVersion()
	if err != nil {
		return readExit("export", err, stderr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.RPCTimeout)
	defer cancel()
	s, err := store.Open(ctx, cfg.DB, store.Options{Passphrase: cfg.Network.Passphrase, ReadOnly: true})
	if err != nil {
		return readExit("export", err, stderr)
	}
	defer func() { _ = s.Close() }()

	var manifest exportManifest
	err = s.Snapshot(ctx, versions, func(ctx context.Context, sn *store.Snapshot) error {
		last, err := sn.State(ctx, store.KeyLastLedger)
		if err != nil {
			return err
		}
		ledger, err := strconv.ParseUint(last, 10, 32)
		if err != nil {
			return fmt.Errorf("sync_state last_ledger %q: %w", last, err)
		}
		manifest = exportManifest{
			Format: "sep47idx-export", FormatVersion: 1, Network: cfg.Network.Name,
			LastLedger: uint32(ledger), SchemaVersion: schema, ParserVersion: sepmeta.ParserVersion,
			RulesetVersions: versions, Files: []exportManifestFile{},
		}
		if err := os.MkdirAll(*out, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", *out, err)
		}
		for _, table := range store.ExportTables {
			rows, err := sn.Read(ctx, table)
			if err != nil {
				return err
			}
			f, count, digest, err := writeExportCSV(filepath.Join(*out, string(table)+".csv"), table, rows)
			_ = rows.Close()
			if err != nil {
				return err
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("write %s.csv: %w", table, err)
			}
			manifest.Files = append(manifest.Files, exportManifestFile{Name: string(table) + ".csv", Rows: count, SHA256: digest})
		}
		return writeManifest(filepath.Join(*out, "manifest.json"), manifest)
	})
	if err != nil {
		return readExit("export", err, stderr)
	}

	if cfg.JSON {
		if err := jsonOutput(stdout, manifest); err != nil {
			return readExit("export", err, stderr)
		}
		return exitOK
	}
	if _, err := fmt.Fprintf(stdout, "Wrote %d files to %s for %s at ledger %d\n", len(manifest.Files), *out, manifest.Network, manifest.LastLedger); err != nil {
		return readExit("export", err, stderr)
	}
	for _, f := range manifest.Files {
		if _, err := fmt.Fprintf(stdout, "  %-14s %8d rows  %s\n", f.Name, f.Rows, f.SHA256); err != nil {
			return readExit("export", err, stderr)
		}
	}
	return exitOK
}

// writeExportCSV streams one table to path and hashes exactly the bytes it
// writes. No row is ever held after it is written, so a million-row table
// costs the same memory as a ten-row one.
func writeExportCSV(path string, table store.ExportTable, rows *sql.Rows) (*os.File, int64, string, error) {
	// #nosec G304 -- the operator names the output directory; the file name
	// is from the fixed export table list.
	f, err := os.Create(path)
	if err != nil {
		return nil, 0, "", fmt.Errorf("create %s: %w", path, err)
	}
	h := sha256.New()
	bw := bufio.NewWriter(io.MultiWriter(f, h))
	w := csv.NewWriter(bw)
	fail := func(err error) (*os.File, int64, string, error) {
		_ = f.Close()
		return nil, 0, "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := w.Write(table.Columns()); err != nil {
		return fail(err)
	}
	cols, err := rows.Columns()
	if err != nil {
		return fail(err)
	}
	if len(cols) != len(table.Columns()) {
		return fail(fmt.Errorf("%s: query returns %d columns, header has %d", table, len(cols), len(table.Columns())))
	}
	values := make([]any, len(cols))
	pointers := make([]any, len(cols))
	for i := range values {
		pointers[i] = &values[i]
	}
	var count int64
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return fail(err)
		}
		record := make([]string, len(values))
		for i, v := range values {
			record[i] = exportValue(v)
		}
		if err := w.Write(record); err != nil {
			return fail(err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fail(err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fail(err)
	}
	if err := bw.Flush(); err != nil {
		return fail(err)
	}
	return f, count, hex.EncodeToString(h.Sum(nil)), nil
}

// exportValue renders a scanned SQLite value as CSV text. NULL is an empty
// field; every nullable column is documented in docs/cli.md, so an empty
// field is never ambiguous about which column it belongs to.
func exportValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}

func writeManifest(path string, m exportManifest) error {
	// #nosec G304 -- path is under the operator-supplied output directory.
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
