package store

import (
	"context"
	"testing"
)

func countExportRows(t *testing.T, sn *Snapshot, table ExportTable) int {
	t.Helper()
	rows, err := sn.Read(context.Background(), table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var n int
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSnapshotIsConsistent proves an export cannot mix states: a write
// committed while the snapshot is open is not visible inside it, and is
// visible after it closes. Without one transaction, the second read would
// see the new contract.
func TestSnapshotIsConsistent(t *testing.T) {
	rw, path := openTest(t)
	ctx := context.Background()
	insert := func(id string) {
		inTx(t, rw, func(tx *Tx) error {
			return tx.UpsertContract(ctx, ContractRow{ID: id, Kind: "wasm"})
		})
	}
	insert("CA")

	ro, err := Open(ctx, path, Options{Passphrase: testnet, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()

	var inside int
	err = ro.Snapshot(ctx, map[int]string{41: "sep41-v0.5.2"}, func(ctx context.Context, sn *Snapshot) error {
		inside = countExportRows(t, sn, ExportContracts)
		insert("CB")
		if got := countExportRows(t, sn, ExportContracts); got != inside {
			t.Errorf("snapshot saw %d contracts after a concurrent write, want %d", got, inside)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if inside != 1 {
		t.Fatalf("snapshot saw %d contracts before the write, want 1", inside)
	}
	var after int
	if err := ro.DB.QueryRow(`SELECT count(*) FROM contracts`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 2 {
		t.Fatalf("after the snapshot: %d contracts, want 2", after)
	}
}

func TestSnapshotRefusesWritableStore(t *testing.T) {
	s, _ := openTest(t)
	err := s.Snapshot(context.Background(), nil, func(context.Context, *Snapshot) error { return nil })
	if err == nil {
		t.Fatal("Snapshot accepted a read-write store; an export must never be able to write")
	}
}
