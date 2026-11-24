package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// VersionResolver returns the newest version of sep that a Wasm with these
// functions implements (a ruleset_version), or "" for none. The matcher's
// match.Versions.Highest implements it; the store only supplies the stored
// functions, so it does not depend on rule files.
type VersionResolver func(sep int, fns []sepmeta.FnSig) string

// Functions returns a Wasm's stored function signatures (functions_json).
// ok is false when none are stored: no spec, or analyzed before migration
// 0002, in which case no version is reported rather than a guessed one.
func (s *Store) Functions(ctx context.Context, hash string) (fns []sepmeta.FnSig, ok bool, err error) {
	var raw sql.NullString
	err = s.DB.QueryRowContext(ctx, `SELECT functions_json FROM wasm WHERE hash = ?`, hash).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !raw.Valid) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := json.Unmarshal([]byte(raw.String), &fns); err != nil {
		return nil, false, fmt.Errorf("functions_json of %s: %w", hash, err)
	}
	return fns, true, nil
}

// AnnotateContract fills matched_version on each inferred claim of the
// contract's current Wasm. It reads; it never changes a status.
func (s *Store) AnnotateContract(ctx context.Context, d *ContractDetail, resolve VersionResolver) error {
	if resolve == nil || d.WasmHash == nil {
		return nil
	}
	fns, ok, err := s.Functions(ctx, *d.WasmHash)
	if err != nil || !ok {
		return err
	}
	for i := range d.Claims {
		if inf := d.Claims[i].Inferred; inf != nil && inf.Status != "no_spec" {
			inf.MatchedVersion = versionPtr(resolve(d.Claims[i].SEP, fns))
		}
	}
	return nil
}

type CoverageResolver func(sep int, fns []sepmeta.FnSig) (cov CoverageSummary, extra []string, ok bool)

func (s *Store) AnnotateWasm(ctx context.Context, d *WasmDetail, resolve VersionResolver) error {
	return s.AnnotateWasmWithCoverage(ctx, d, resolve, nil)
}

func (s *Store) AnnotateWasmWithCoverage(ctx context.Context, d *WasmDetail, resolve VersionResolver, covResolve CoverageResolver) error {
	if len(d.Matches) == 0 {
		return nil
	}
	fns, ok, err := s.Functions(ctx, d.Hash)
	if err != nil || !ok {
		return err
	}
	for i := range d.Matches {
		if d.Matches[i].Status != "no_spec" {
			if resolve != nil {
				d.Matches[i].MatchedVersion = versionPtr(resolve(d.Matches[i].SEP, fns))
			}
			if covResolve != nil {
				cov, extra, cok := covResolve(d.Matches[i].SEP, fns)
				if cok {
					d.Matches[i].Extra = extra
					d.Matches[i].Coverage = &cov
				}
			}
		}
	}
	return nil
}

func versionPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
