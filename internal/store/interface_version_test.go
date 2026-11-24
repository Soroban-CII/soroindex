package store

import (
	"context"
	"strings"
	"testing"
)

// TestWasmInterfaceVersionNullable checks the two states the column must
// keep apart: an absent contractenvmetav0 section is NULL, and a decoded
// version is stored and read back. Archival must not clear either.
func TestWasmInterfaceVersionNullable(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()

	inTx(t, s, func(tx *Tx) error {
		return tx.UpsertWasm(ctx, WasmRow{Hash: strings.Repeat("a", 64), ParseStatus: "ok", ParserVersion: "1"})
	})
	if got := queryString(t, s, `SELECT interface_version FROM wasm WHERE hash = ?`, strings.Repeat("a", 64)); got != "<nil>" {
		t.Fatalf("absent section stored %s, want NULL", got)
	}

	version, pre := uint32(28), uint32(0)
	inTx(t, s, func(tx *Tx) error {
		return tx.UpsertWasm(ctx, WasmRow{Hash: strings.Repeat("b", 64), ParseStatus: "ok", ParserVersion: "1",
			InterfaceVersion: &version, InterfacePrerelease: &pre})
	})
	d, err := s.Wasm(ctx, strings.Repeat("b", 64), 10, "", map[int]string{})
	if err != nil {
		t.Fatal(err)
	}
	if d.InterfaceVersion == nil || *d.InterfaceVersion != 28 || d.InterfacePrerelease == nil || *d.InterfacePrerelease != 0 {
		t.Fatalf("interface version = %v / %v, want 28 / 0", d.InterfaceVersion, d.InterfacePrerelease)
	}

	inTx(t, s, func(tx *Tx) error {
		return tx.UpsertWasm(ctx, WasmRow{Hash: strings.Repeat("b", 64), ParseStatus: "archived", ParserVersion: "1"})
	})
	if got := queryString(t, s, `SELECT interface_version || '/' || interface_prerelease FROM wasm WHERE hash = ?`, strings.Repeat("b", 64)); got != "28/0" {
		t.Fatalf("archival cleared the interface version: %s", got)
	}
}
