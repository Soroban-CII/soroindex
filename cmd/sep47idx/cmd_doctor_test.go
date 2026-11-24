package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/config"
)

func doctorRPC(t *testing.T, passphrase string, latest uint32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var result any
		switch req.Method {
		case "getNetwork":
			result = map[string]any{"passphrase": passphrase, "protocolVersion": 29}
		case "getLatestLedger":
			result = map[string]any{"sequence": latest}
		default:
			t.Errorf("unexpected method %s", req.Method)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func doctorReportOf(t *testing.T, out []byte) doctorReport {
	t.Helper()
	var r doctorReport
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	return r
}

func doctorCheckStatus(r doctorReport, name string) string {
	for _, c := range r.Checks {
		if c.Name == name {
			return c.Status
		}
	}
	return "<missing>"
}

func TestDoctorPassesHealthyIndex(t *testing.T) {
	db, _, _ := readFixture(t)
	srv := doctorRPC(t, config.Testnet.Passphrase, 12)
	t.Setenv("SEP47IDX_DB", db)
	t.Setenv("SEP47IDX_RPC_URL", srv.URL)

	var out, errb bytes.Buffer
	if code := run([]string{"doctor", "--json"}, &out, &errb); code != exitOK {
		t.Fatalf("code = %d, stderr = %s, out = %s", code, errb.String(), out.String())
	}
	r := doctorReportOf(t, out.Bytes())
	if !r.OK || len(r.Checks) != 7 {
		t.Fatalf("report = %+v", r)
	}
	for _, c := range r.Checks {
		if c.Status != doctorPass || c.Detail == "" {
			t.Fatalf("check %s = %s: %s", c.Name, c.Status, c.Detail)
		}
	}
}

func TestDoctorReportsMissingDatabase(t *testing.T) {
	srv := doctorRPC(t, config.Testnet.Passphrase, 12)
	t.Setenv("SEP47IDX_DB", t.TempDir()+"/absent.db")
	t.Setenv("SEP47IDX_RPC_URL", srv.URL)

	var out, errb bytes.Buffer
	if code := run([]string{"doctor", "--json"}, &out, &errb); code != exitError {
		t.Fatalf("code = %d, want %d; out = %s", code, exitError, out.String())
	}
	r := doctorReportOf(t, out.Bytes())
	if r.OK || doctorCheckStatus(r, "database") != doctorFail {
		t.Fatalf("report = %+v", r)
	}
	if doctorCheckStatus(r, "rpc") != doctorPass {
		t.Fatalf("RPC should still be checked and pass: %+v", r)
	}
	if doctorCheckStatus(r, "sync_state") != "<missing>" {
		t.Fatalf("sync_state should be skipped when the database did not open: %+v", r)
	}
	if !strings.Contains(out.String(), "sep47idx sync") {
		t.Fatalf("missing fix suggestion: %s", out.String())
	}
}

func TestDoctorReportsWrongNetwork(t *testing.T) {
	db, _, _ := readFixture(t) // records the testnet passphrase
	srv := doctorRPC(t, config.Mainnet.Passphrase, 12)
	t.Setenv("SEP47IDX_DB", db)
	t.Setenv("SEP47IDX_NETWORK", "mainnet")
	t.Setenv("SEP47IDX_RPC_URL", srv.URL)

	var out, errb bytes.Buffer
	if code := run([]string{"doctor", "--json"}, &out, &errb); code != exitError {
		t.Fatalf("code = %d, out = %s", code, out.String())
	}
	r := doctorReportOf(t, out.Bytes())
	if doctorCheckStatus(r, "database") != doctorFail || !strings.Contains(out.String(), "matching --network") {
		t.Fatalf("report = %+v", r)
	}
}

func TestDoctorReportsWrongRPCNetwork(t *testing.T) {
	db, _, _ := readFixture(t)
	srv := doctorRPC(t, config.Mainnet.Passphrase, 12)
	t.Setenv("SEP47IDX_DB", db)
	t.Setenv("SEP47IDX_RPC_URL", srv.URL)

	var out, errb bytes.Buffer
	if code := run([]string{"doctor", "--json"}, &out, &errb); code != exitError {
		t.Fatalf("code = %d, out = %s", code, out.String())
	}
	r := doctorReportOf(t, out.Bytes())
	if doctorCheckStatus(r, "rpc") != doctorFail || !strings.Contains(out.String(), "point --rpc-url") {
		t.Fatalf("report = %+v", r)
	}
	if doctorCheckStatus(r, "lag") != doctorSkip {
		t.Fatalf("lag should be skipped without a tip: %+v", r)
	}
}

func TestDoctorReportsUnreachableRPC(t *testing.T) {
	db, _, _ := readFixture(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	t.Setenv("SEP47IDX_DB", db)
	t.Setenv("SEP47IDX_RPC_URL", url)

	var out, errb bytes.Buffer
	if code := run([]string{"doctor", "--json"}, &out, &errb); code != exitError {
		t.Fatalf("code = %d, out = %s", code, out.String())
	}
	r := doctorReportOf(t, out.Bytes())
	if doctorCheckStatus(r, "rpc") != doctorFail {
		t.Fatalf("report = %+v", r)
	}
}

func TestDoctorReportsDiskAndLagThresholds(t *testing.T) {
	db, _, _ := readFixture(t)
	srv := doctorRPC(t, config.Testnet.Passphrase, 1000)
	t.Setenv("SEP47IDX_DB", db)
	t.Setenv("SEP47IDX_RPC_URL", srv.URL)

	var out, errb bytes.Buffer
	if code := run([]string{"doctor", "--json", "--min-free-mb", "999999999", "--max-lag", "5"}, &out, &errb); code != exitError {
		t.Fatalf("code = %d, out = %s", code, out.String())
	}
	r := doctorReportOf(t, out.Bytes())
	for _, name := range []string{"disk", "lag"} {
		if doctorCheckStatus(r, name) != doctorFail {
			t.Fatalf("%s should fail: %+v", name, r)
		}
	}
	if !strings.Contains(out.String(), "free space") || !strings.Contains(out.String(), "follow process") {
		t.Fatalf("missing fixes: %s", out.String())
	}
}

func TestDoctorDoesNotWriteDatabase(t *testing.T) {
	db, _, _ := readFixture(t)
	srv := doctorRPC(t, config.Testnet.Passphrase, 12)
	t.Setenv("SEP47IDX_DB", db)
	t.Setenv("SEP47IDX_RPC_URL", srv.URL)

	before, err := os.ReadFile(db) // #nosec G304 -- test-owned temp database
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(before)
	if code := run([]string{"doctor", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}); code != exitOK {
		t.Fatalf("code = %d", code)
	}
	after, err := os.ReadFile(db) // #nosec G304 -- test-owned temp database
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(after); got != sum {
		t.Fatalf("doctor changed the database: %s != %s", hex.EncodeToString(got[:]), hex.EncodeToString(sum[:]))
	}
}

func TestDoctorRejectsArguments(t *testing.T) {
	t.Setenv("SEP47IDX_DB", t.TempDir()+"/absent.db")
	var out, errb bytes.Buffer
	if code := run([]string{"doctor", "extra"}, &out, &errb); code != exitError {
		t.Fatalf("code = %d", code)
	}
}
