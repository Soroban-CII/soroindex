package ingest

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/Soroban-CII/soroindex/internal/store"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// ApplyLedger applies ordered ledger facts inside the caller's batch transaction.
// Malformed facts fail the batch instead of advancing past unindexed changes.
func (ix *Indexer) ApplyLedger(ctx context.Context, tx *store.Tx, f ContractFacts) error {
	if len(f.Skipped) > 0 {
		return fmt.Errorf("ledger %d: %w", f.Ledger, errors.Join(f.Skipped...))
	}
	for _, c := range f.Changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.Kind == Removed || c.Kind == Evicted {
			if c.Key == nil {
				return ErrMalformed
			}
			if err := ix.removeEntry(ctx, tx, *c.Key, f.Ledger); err != nil {
				return err
			}
			continue
		}
		if c.Entry == nil {
			return ErrMalformed
		}
		d := c.Entry.Data
		switch d.Type {
		case xdr.LedgerEntryTypeContractCode:
			if d.ContractCode == nil {
				return ErrMalformed
			}
			code := d.ContractCode
			hash := hex.EncodeToString(code.Hash[:])
			have, err := tx.HasWasm(ctx, hash)
			if err != nil {
				return err
			}
			// Restored entries are analyzed again even when an archived row exists.
			neverParsed, err := tx.WasmArchived(ctx, hash)
			if err != nil {
				return err
			}
			if !have || neverParsed || c.Kind == Restored {
				if err := ix.writeAnalysis(ctx, tx, hash, code.Code, f.Ledger); err != nil {
					return err
				}
			}
			if err := tx.SetCodeArchived(ctx, hash, false); err != nil {
				return err
			}
		case xdr.LedgerEntryTypeContractData:
			if d.ContractData == nil {
				return ErrMalformed
			}
			cd := *d.ContractData
			if IsExecRefKey(cd) {
				ref, err := DecodeExecRef(cd)
				if err != nil {
					return err
				}
				if err := ix.applyReference(ctx, tx, ref, f.Ledger, false); err != nil {
					return err
				}
			} else if IsInstanceKey(cd) {
				in, err := DecodeInstance(cd)
				if err != nil {
					return err
				}
				row := store.ContractRow{ID: in.ContractID, Kind: string(in.Kind), CurrentWasmHash: in.WasmHash, ExecRefOwner: in.RefOwner, ExecRefTag: in.RefTag, SACAsset: in.SACAsset, UpdatedLedger: f.Ledger}
				if c.Kind == Created {
					row.CreatedLedger = f.Ledger
				}
				if in.Kind == KindWasmRef {
					hash, archived, err := ix.resolveReference(ctx, tx, in.RefOwner, in.RefTag, f.Ledger)
					if err != nil {
						return err
					}
					row.CurrentWasmHash, row.Archived = hash, archived
				}
				if row.CurrentWasmHash != "" {
					if _, err := ix.EnsureWasm(ctx, tx, []string{row.CurrentWasmHash}, f.Ledger); err != nil {
						return err
					}
					codeArchived, err := tx.WasmArchived(ctx, row.CurrentWasmHash)
					if err != nil {
						return err
					}
					row.Archived = row.Archived || codeArchived
				}
				// A seed may already reflect a newer instance. Do not regress it while replaying.
				old, err := tx.Contract(ctx, row.ID)
				if err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
				if err == nil && old.UpdatedLedger > f.Ledger {
					continue
				}
				if _, err := ix.ApplyContract(ctx, tx, row, f.Ledger); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (ix *Indexer) resolveReference(ctx context.Context, tx *store.Tx, owner, tag string, ledger uint32) (string, bool, error) {
	hash, archived, err := tx.ExecRef(ctx, owner, tag)
	if !errors.Is(err, store.ErrNotFound) {
		return hash, archived, err
	}
	key, err := ExecRefKey(owner, tag)
	if err != nil {
		return "", false, err
	}
	res, err := ix.RPC.GetLedgerEntries(ctx, []string{key})
	if err != nil {
		return "", false, err
	}
	for _, e := range res.Entries {
		if e.Key != key {
			return "", false, ErrMalformed
		}
		// RPC serves current state. A later reference cannot establish historical code.
		if e.LastModifiedLedgerSeq > ledger {
			continue
		}
		data, err := DecodeLedgerEntryDataBase64(e.XDR, XDRLimits{})
		if err != nil {
			return "", false, err
		}
		if data.ContractData == nil {
			return "", false, ErrMalformed
		}
		ref, err := DecodeExecRef(*data.ContractData)
		if err != nil {
			return "", false, err
		}
		if ref.Owner != owner || ref.Tag != tag {
			return "", false, ErrMalformed
		}
		archived := e.LiveUntilLedgerSeq != nil && *e.LiveUntilLedgerSeq < res.LatestLedger
		if err := tx.UpsertExecRef(ctx, owner, tag, ref.WasmHash, e.LastModifiedLedgerSeq, archived); err != nil {
			return "", false, err
		}
		if archived {
			return "", true, nil
		}
		return ref.WasmHash, false, nil
	}
	return "", true, nil
}

func (ix *Indexer) applyReference(ctx context.Context, tx *store.Tx, ref ExecRef, ledger uint32, archived bool) error {
	if err := tx.UpsertExecRef(ctx, ref.Owner, ref.Tag, ref.WasmHash, ledger, archived); err != nil {
		return err
	}
	hash, archived, err := tx.ExecRef(ctx, ref.Owner, ref.Tag)
	if err != nil {
		return err
	}
	if hash != "" {
		if _, err := ix.EnsureWasm(ctx, tx, []string{hash}, ledger); err != nil {
			return err
		}
	}
	ids, err := tx.Referencing(ctx, ref.Owner, ref.Tag)
	if err != nil {
		return err
	}
	for _, id := range ids {
		row, err := tx.Contract(ctx, id)
		if err != nil {
			return err
		}
		if row.UpdatedLedger > ledger {
			continue
		}
		row.CurrentWasmHash, row.Archived, row.UpdatedLedger = hash, archived, ledger
		if _, err := ix.ApplyContract(ctx, tx, row, ledger); err != nil {
			return err
		}
	}
	return nil
}

func (ix *Indexer) removeEntry(ctx context.Context, tx *store.Tx, key xdr.LedgerKey, ledger uint32) error {
	if key.ContractCode != nil {
		return tx.SetCodeArchived(ctx, hex.EncodeToString(key.ContractCode.Hash[:]), true)
	}
	if key.ContractData == nil {
		return nil
	}
	cd := key.ContractData
	id, err := ContractIDString(cd.Contract)
	if err != nil {
		return err
	}
	if cd.Key.Type == xdr.ScValTypeScvLedgerKeyContractInstance {
		return tx.SetContractArchived(ctx, id, true)
	}
	if cd.Key.Type == xdr.ScValTypeScvExecutableTag {
		tag, ok := cd.Key.GetExecutableTag()
		if !ok {
			return ErrMalformed
		}
		return ix.applyReference(ctx, tx, ExecRef{Owner: id, Tag: string(tag)}, ledger, true)
	}
	return nil
}
