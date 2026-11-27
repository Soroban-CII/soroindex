package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
	"github.com/Soroban-CII/soroindex/internal/store"
)

// paddedFixture writes a golden fixture with an extra custom section of pad
// bytes appended, so the module is larger than the default total Wasm limit
// while still declaring what the fixture declares. The padding section is
// generated here rather than committed, so no large binary enters the repo.
func paddedFixture(t *testing.T, name string, pad int) string {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("../../testdata/wasm", name)) // #nosec G304 -- fixed fixture name
	if err != nil {
		t.Fatal(err)
	}
	// A custom section: id 0, LEB128 payload size, LEB128 name length, name,
	// then the payload. The reader skips sections it was not asked for.
	const sectionName = "sep47idx.padding"
	body := append([]byte{byte(len(sectionName))}, sectionName...)
	body = append(body, make([]byte, pad)...)
	out := append([]byte{}, code...)
	out = append(out, 0)
	for n := len(body); ; {
		b := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			break
		}
	}
	out = append(out, body...)
	path := filepath.Join(t.TempDir(), "padded.wasm")
	if err := os.WriteFile(path, out, 0o600); err != nil { // #nosec G703 -- path is this test's t.TempDir()
		t.Fatal(err)
	}
	return path
}

// TestInspectCLIRaisedWasmLimitAcceptsALargeModule is the end-to-end proof
// that a configured limit reaches the parser: the same file is refused at the
// default and read when the limit is raised.
func TestInspectCLIRaisedWasmLimitAcceptsALargeModule(t *testing.T) {
	path := paddedFixture(t, "token_full_sep.wasm", 1<<20) // over the 1 MiB default

	t.Run("the default limit refuses the module and names the limit", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{"inspect", path}, &out, &errb); code != exitError {
			t.Fatalf("exit %d, want 1: %s", code, errb.String())
		}
		if !strings.Contains(errb.String(), "limit") {
			t.Fatalf("error does not mention the limit: %s", errb.String())
		}
	})

	t.Run("a raised --max-wasm-bytes reads the same module", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{"inspect", path, "--max-wasm-bytes", "4MiB", "--json"}, &out, &errb); code != exitOK {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		var got struct {
			Claims []struct {
				SEP int `json:"sep"`
			} `json:"claims"`
			ParseStatus string `json:"parse_status"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("%v: %s", err, out.String())
		}
		if got.ParseStatus != "ok" || len(got.Claims) != 1 || got.Claims[0].SEP != 41 {
			t.Fatalf("parse %q claims %+v, want ok and SEP-41", got.ParseStatus, got.Claims)
		}
	})

	t.Run("the same limit is settable by environment variable", func(t *testing.T) {
		t.Setenv("SEP47IDX_MAX_WASM_BYTES", "4MiB")
		var out, errb bytes.Buffer
		if code := run([]string{"inspect", path}, &out, &errb); code != exitOK {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		if !strings.Contains(out.String(), "Declared SEP-41 (sep47-meta)") {
			t.Fatalf("unexpected output: %s", out.String())
		}
	})

	t.Run("an over-cap value is refused before the file is read", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{"inspect", "does-not-exist.wasm", "--max-wasm-bytes", "65MiB"}, &out, &errb); code != exitError {
			t.Fatalf("exit %d, want 1", code)
		}
		// The limit, not the missing file, is what is reported: validation
		// happens before any input is opened.
		if !strings.Contains(errb.String(), "--max-wasm-bytes") || strings.Contains(errb.String(), "does-not-exist") {
			t.Fatalf("want the limit refused first, got: %s", errb.String())
		}
	})
}

func TestSyncCLIRejectsOutOfRangeLimitsBeforeNetwork(t *testing.T) {
	db := filepath.Join(t.TempDir(), "testnet.db")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"a Wasm limit over the hard cap", []string{"--max-wasm-bytes", "65MiB"}, "--max-wasm-bytes"},
		{"a zero Wasm limit", []string{"--max-wasm-bytes", "0"}, "at least 1 byte"},
		{"an unparseable size", []string{"--max-section-bytes", "lots"}, "want a byte count"},
		{"a zero section count", []string{"--max-sections", "0"}, "--max-sections"},
		{"an XDR depth over the decoder's maximum", []string{"--max-xdr-depth", "1501"}, "--max-xdr-depth"},
		{"an XDR string ceiling over the response cap", []string{"--max-xdr-base64-bytes", "257MiB"}, "--max-xdr-base64-bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name+" is refused", func(t *testing.T) {
			var out, errb bytes.Buffer
			// --rpc-url points nowhere: a run that reached the network would
			// fail with a connection error instead of the limit error.
			args := append([]string{"sync", "--db", db, "--rpc-url", "http://127.0.0.1:1", "--start-ledger", "10"}, tt.args...)
			if code := run(args, &out, &errb); code != exitError {
				t.Fatalf("exit %d, want 1: %s", code, errb.String())
			}
			if !strings.Contains(errb.String(), tt.want) {
				t.Fatalf("error %q does not contain %q", errb.String(), tt.want)
			}
			if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Fatalf("a refused limit created the database: %v", err)
			}
		})
	}
}

func TestSyncCLIRecordsTheLimitsItRanWith(t *testing.T) {
	server := syncRPC(t, 1)
	db := filepath.Join(t.TempDir(), "testnet.db")
	var out, errb bytes.Buffer
	args := []string{"sync", "--db", db, "--rpc-url", server.URL, "--start-ledger", "10",
		"--max-wasm-bytes", "2MiB", "--max-section-bytes", "512KiB", "--max-sections", "4096",
		"--max-xdr-depth", "250", "--max-xdr-base64-bytes", "32MiB"}
	if code := run(args, &out, &errb); code != exitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	s, err := store.Open(context.Background(), db, store.Options{Passphrase: config.Testnet.Passphrase, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	v, err := s.State(context.Background(), store.KeyLimits)
	if err != nil {
		t.Fatalf("sync recorded no limits: %v", err)
	}
	got, err := config.ParseLimitsJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	want := config.Limits{MaxWasmBytes: 2 << 20, MaxSectionBytes: 512 << 10, MaxSections: 4096, MaxXDRDepth: 250, MaxXDRBase64Bytes: 32 << 20}
	if got != want {
		t.Fatalf("recorded %+v, want %+v", got, want)
	}

	t.Run("stats reports the recorded limits in text and JSON", func(t *testing.T) {
		t.Setenv("SEP47IDX_DB", db)
		var out, errb bytes.Buffer
		if code := run([]string{"stats"}, &out, &errb); code != exitOK {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		if !strings.Contains(out.String(), "Limits (recorded by the last sync): wasm 2MiB, section 512KiB, sections 4096, xdr depth 250, xdr base64 32MiB") {
			t.Fatalf("stats did not report the limits: %s", out.String())
		}
		out.Reset()
		if code := run([]string{"stats", "--json"}, &out, &errb); code != exitOK {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		var decoded struct {
			Limits *config.Limits `json:"limits"`
		}
		if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Limits == nil || *decoded.Limits != want {
			t.Fatalf("stats JSON limits = %+v, want %+v", decoded.Limits, want)
		}
	})
}

func TestStatsCLISaysSoWhenNoLimitsWereRecorded(t *testing.T) {
	db, _, _ := readFixture(t) // built directly through the store, as an older binary's index would be
	t.Setenv("SEP47IDX_DB", db)
	var out, errb bytes.Buffer
	if code := run([]string{"stats"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "Limits: not recorded") {
		t.Fatalf("stats invented limits: %s", out.String())
	}
	out.Reset()
	if code := run([]string{"stats", "--json"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if strings.Contains(out.String(), `"limits"`) {
		t.Fatalf("stats JSON reports limits that were never recorded: %s", out.String())
	}
}

// TestSyncCLIDefaultsRecordTheShippedLimits is the backward-compatibility
// proof at the command level: a run that passes no limit flag records, and
// therefore uses, exactly the limits every earlier release used.
func TestSyncCLIDefaultsRecordTheShippedLimits(t *testing.T) {
	server := syncRPC(t, 1)
	db := filepath.Join(t.TempDir(), "testnet.db")
	var out, errb bytes.Buffer
	if code := run([]string{"sync", "--db", db, "--rpc-url", server.URL, "--start-ledger", "10"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	s, err := store.Open(context.Background(), db, store.Options{Passphrase: config.Testnet.Passphrase, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	v, err := s.State(context.Background(), store.KeyLimits)
	if err != nil {
		t.Fatal(err)
	}
	got, err := config.ParseLimitsJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	if got != config.DefaultLimits() {
		t.Fatalf("recorded %+v, want the shipped defaults %+v", got, config.DefaultLimits())
	}
}
