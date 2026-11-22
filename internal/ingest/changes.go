package ingest

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// ChangeKind is what happened to a ledger entry.
type ChangeKind uint8

// Change kinds. LedgerEntryState pre-images are not reported: they describe
// the entry before a change, which the following change supersedes.
const (
	Created  ChangeKind = iota + 1 // entry created
	Updated                        // entry updated
	Removed                        // entry deleted
	Restored                       // entry restored from the archive
	Evicted                        // entry archived by eviction at this ledger
)

// Change is one ledger entry change, in the order the network applied it.
// Entry is the post-change entry for Created, Updated and Restored; Key is
// set for Removed and Evicted.
//
// TxHash and SourceAccount are non-empty when the change originated in a
// transaction (as opposed to a ledger upgrade or eviction). TxHash is the
// lowercase hex transaction hash. SourceAccount is the G-address of the
// transaction source account, populated where the metadata exposes it.
type Change struct {
	Kind          ChangeKind
	Entry         *xdr.LedgerEntry
	Key           *xdr.LedgerKey
	TxHash        string
	SourceAccount string
}

// ErrUnsupportedMeta means a LedgerCloseMeta or TransactionMeta version this
// code does not know. It is an error, not a skip: silently missing changes
// would leave the index wrong without saying so.
var ErrUnsupportedMeta = errors.New("unsupported meta version")

// txProvenance holds the transaction hash and source account for one
// transaction in processing order.
type txProvenance struct {
	Hash   string // lowercase hex
	Source string // G-address; empty when unavailable
}

// provenance returns per-transaction provenance for m in processing order.
// TxHash is always populated. SourceAccount is populated for V0 (where the
// transaction set is ordered to match TxProcessing) and left empty for V1/V2
// (where the phase layout does not expose a stable per-index mapping).
func provenance(m xdr.LedgerCloseMeta) []txProvenance {
	n := m.CountTransactions()
	out := make([]txProvenance, n)
	for i := range out {
		h := m.TransactionHash(i)
		out[i].Hash = hex.EncodeToString(h[:])
	}
	// V0: TxSet.Txs is in the same order as TxProcessing.
	if m.V == 0 && m.V0 != nil {
		for i, env := range m.V0.TxSet.Txs {
			if i < n {
				out[i].Source = env.SourceAccount().ToAccountId().Address()
			}
		}
	}
	return out
}

// Changes returns every ledger entry change in a ledger, in apply order:
// for each transaction its fee processing, then its changes before, during
// and after its operations, then any post-apply fee changes; then upgrade
// changes; then evictions.
//
// Changes from transactions carry TxHash (lowercase hex). SourceAccount is
// populated where the metadata exposes it (V0 ledgers); it is empty for V1
// and V2 ledgers. Upgrade and eviction changes leave both fields empty.
func Changes(m xdr.LedgerCloseMeta) ([]Change, error) {
	// Validate version and body before calling provenance: CountTransactions
	// panics for unknown versions or a known version with a nil body.
	switch m.V {
	case 0:
		if m.V0 == nil {
			return nil, fmt.Errorf("%w: v0 body missing", ErrUnsupportedMeta)
		}
	case 1:
		if m.V1 == nil {
			return nil, fmt.Errorf("%w: v1 body missing", ErrUnsupportedMeta)
		}
	case 2:
		if m.V2 == nil {
			return nil, fmt.Errorf("%w: v2 body missing", ErrUnsupportedMeta)
		}
	default:
		return nil, fmt.Errorf("%w: LedgerCloseMeta v%d", ErrUnsupportedMeta, m.V)
	}
	provs := provenance(m)
	var out []Change
	addWith := func(cs xdr.LedgerEntryChanges, p txProvenance) {
		for i := range cs {
			c := &cs[i]
			switch c.Type {
			case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
				out = append(out, Change{Kind: Created, Entry: c.Created, TxHash: p.Hash, SourceAccount: p.Source})
			case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
				out = append(out, Change{Kind: Updated, Entry: c.Updated, TxHash: p.Hash, SourceAccount: p.Source})
			case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
				out = append(out, Change{Kind: Removed, Key: c.Removed, TxHash: p.Hash, SourceAccount: p.Source})
			case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
				out = append(out, Change{Kind: Restored, Entry: c.Restored, TxHash: p.Hash, SourceAccount: p.Source})
			}
		}
	}
	add := func(cs xdr.LedgerEntryChanges) { addWith(cs, txProvenance{}) }
	addTx := func(fee xdr.LedgerEntryChanges, tm xdr.TransactionMeta, postFee xdr.LedgerEntryChanges, p txProvenance) error {
		addWith(fee, p)
		if err := addTxMeta(tm, func(cs xdr.LedgerEntryChanges) { addWith(cs, p) }); err != nil {
			return err
		}
		addWith(postFee, p)
		return nil
	}
	var upgrades []xdr.UpgradeEntryMeta
	var evicted []xdr.LedgerKey
	switch m.V {
	case 0:
		if m.V0 == nil {
			return nil, fmt.Errorf("%w: v0 body missing", ErrUnsupportedMeta)
		}
		for i, tx := range m.V0.TxProcessing {
			p := provs[i]
			if err := addTx(tx.FeeProcessing, tx.TxApplyProcessing, nil, p); err != nil {
				return nil, err
			}
		}
		upgrades = m.V0.UpgradesProcessing
	case 1:
		if m.V1 == nil {
			return nil, fmt.Errorf("%w: v1 body missing", ErrUnsupportedMeta)
		}
		for i, tx := range m.V1.TxProcessing {
			if err := addTx(tx.FeeProcessing, tx.TxApplyProcessing, nil, provs[i]); err != nil {
				return nil, err
			}
		}
		upgrades, evicted = m.V1.UpgradesProcessing, m.V1.EvictedKeys
	case 2:
		if m.V2 == nil {
			return nil, fmt.Errorf("%w: v2 body missing", ErrUnsupportedMeta)
		}
		for i, tx := range m.V2.TxProcessing {
			if err := addTx(tx.FeeProcessing, tx.TxApplyProcessing, tx.PostTxApplyFeeProcessing, provs[i]); err != nil {
				return nil, err
			}
		}
		upgrades, evicted = m.V2.UpgradesProcessing, m.V2.EvictedKeys
	default:
		return nil, fmt.Errorf("%w: LedgerCloseMeta v%d", ErrUnsupportedMeta, m.V)
	}
	for _, u := range upgrades {
		add(u.Changes)
	}
	for i := range evicted {
		out = append(out, Change{Kind: Evicted, Key: &evicted[i]})
	}
	return out, nil
}

func addTxMeta(tm xdr.TransactionMeta, add func(xdr.LedgerEntryChanges)) error {
	switch tm.V {
	case 0:
		if tm.Operations != nil {
			for _, op := range *tm.Operations {
				add(op.Changes)
			}
		}
	case 1:
		if tm.V1 == nil {
			return fmt.Errorf("%w: tx meta v1 body missing", ErrUnsupportedMeta)
		}
		add(tm.V1.TxChanges)
		for _, op := range tm.V1.Operations {
			add(op.Changes)
		}
	case 2:
		if tm.V2 == nil {
			return fmt.Errorf("%w: tx meta v2 body missing", ErrUnsupportedMeta)
		}
		add(tm.V2.TxChangesBefore)
		for _, op := range tm.V2.Operations {
			add(op.Changes)
		}
		add(tm.V2.TxChangesAfter)
	case 3:
		if tm.V3 == nil {
			return fmt.Errorf("%w: tx meta v3 body missing", ErrUnsupportedMeta)
		}
		add(tm.V3.TxChangesBefore)
		for _, op := range tm.V3.Operations {
			add(op.Changes)
		}
		add(tm.V3.TxChangesAfter)
	case 4:
		if tm.V4 == nil {
			return fmt.Errorf("%w: tx meta v4 body missing", ErrUnsupportedMeta)
		}
		add(tm.V4.TxChangesBefore)
		for _, op := range tm.V4.Operations {
			add(op.Changes)
		}
		add(tm.V4.TxChangesAfter)
	default:
		return fmt.Errorf("%w: TransactionMeta v%d", ErrUnsupportedMeta, tm.V)
	}
	return nil
}

// ContractFacts are the contract-level facts in one ledger, in apply order.
type ContractFacts struct {
	Ledger uint32
	// Changes preserves apply order across instances, references, code and
	// removals. Incremental sync must not apply the grouped summaries below.
	Changes []Change
	// CodeCreated lists Wasm hashes (hex) whose code entry was created or
	// restored.
	CodeCreated []string
	// Instances lists instance entries created, updated or restored, with
	// Created true only for a creation.
	Instances []InstanceChange
	// ExecRefs lists CAP-85 reference entries created, updated or restored.
	ExecRefs []ExecRef
	// Evicted lists keys archived at this ledger.
	Evicted []xdr.LedgerKey
	// Skipped counts contract data entries in an instance or reference slot
	// that failed to decode. They are reported, never silently dropped.
	Skipped []error
}

// InstanceChange is an instance entry as of a change.
type InstanceChange struct {
	Instance
	Created bool
}

// ExtractContractFacts filters a ledger's changes down to contract code,
// contract instances and executable references.
func ExtractContractFacts(m xdr.LedgerCloseMeta) (ContractFacts, error) {
	changes, err := Changes(m)
	if err != nil {
		return ContractFacts{}, err
	}
	f := ContractFacts{Ledger: m.LedgerSequence()}
	for _, c := range changes {
		// Keep only relevant changes, including removals and restorations.
		if c.Key != nil {
			if c.Key.Type == xdr.LedgerEntryTypeContractCode || (c.Key.ContractData != nil &&
				c.Key.ContractData.Durability == xdr.ContractDataDurabilityPersistent &&
				(c.Key.ContractData.Key.Type == xdr.ScValTypeScvLedgerKeyContractInstance || c.Key.ContractData.Key.Type == xdr.ScValTypeScvExecutableTag)) {
				f.Changes = append(f.Changes, c)
			}
		} else if c.Entry != nil {
			d := c.Entry.Data
			if d.Type == xdr.LedgerEntryTypeContractCode || (d.ContractData != nil && (IsInstanceKey(*d.ContractData) || IsExecRefKey(*d.ContractData))) {
				f.Changes = append(f.Changes, c)
			}
		}
		switch c.Kind {
		case Evicted:
			f.Evicted = append(f.Evicted, *c.Key)
			continue
		case Removed:
			continue
		}
		d := c.Entry.Data
		switch d.Type {
		case xdr.LedgerEntryTypeContractCode:
			if c.Kind == Created || c.Kind == Restored {
				f.CodeCreated = append(f.CodeCreated, hex.EncodeToString(d.ContractCode.Hash[:]))
			}
		case xdr.LedgerEntryTypeContractData:
			cd := *d.ContractData
			switch {
			case IsInstanceKey(cd):
				inst, err := DecodeInstance(cd)
				if err != nil {
					f.Skipped = append(f.Skipped, err)
					continue
				}
				f.Instances = append(f.Instances, InstanceChange{Instance: inst, Created: c.Kind == Created})
			case IsExecRefKey(cd):
				ref, err := DecodeExecRef(cd)
				if err != nil {
					f.Skipped = append(f.Skipped, err)
					continue
				}
				f.ExecRefs = append(f.ExecRefs, ref)
			}
		}
	}
	return f, nil
}
