package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/store"
)

func pageOf(t *testing.T, s *Server, query string) (ids []string, next string) {
	t.Helper()
	w := serve(t, s, "/v1/contracts?"+query)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
	}
	var p store.Page
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Contracts {
		ids = append(ids, c.ID)
	}
	if p.NextCursor != nil {
		next = *p.NextCursor
	}
	return ids, next
}

func errorBody(t *testing.T, s *Server, path string) (int, string, string) {
	t.Helper()
	w := serve(t, s, path)
	var e struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	return w.Code, e.Error.Code, e.Error.Message
}

// TestPagingDuringSyncNeverRepeatsOrSkipsOriginalRows pages /v1/contracts one
// row at a time while a writer commits new contracts before and after the
// current position between every request.
func TestPagingDuringSyncNeverRepeatsOrSkipsOriginalRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sync.db")
	w, err := store.Open(ctx, path, store.Options{Passphrase: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	insert := func(n byte) string {
		id := catalogID(t, n)
		tx, err := w.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.UpsertContract(ctx, store.ContractRow{ID: id, Kind: "sac", UpdatedLedger: 100}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return id
	}
	var original []string
	for n := byte(1); n <= 12; n++ {
		original = append(original, insert(n))
	}
	sort.Strings(original)
	ro, err := store.Open(ctx, path, store.Options{Passphrase: "test", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	s, err := New(Options{Store: ro, Network: "testnet"})
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	query := "tier=protocol&limit=1"
	next := byte(100)
	for {
		ids, cursor := pageOf(t, s, query)
		got = append(got, ids...)
		if cursor == "" {
			break
		}
		// Two new rows per page: one sorts before the cursor row, one after it.
		last := ids[len(ids)-1]
		var before, after bool
		for !(before && after) {
			id := insert(next)
			next++
			if id < last {
				before = true
			} else {
				after = true
			}
		}
		query = "tier=protocol&limit=1&cursor=" + url.QueryEscape(cursor)
	}
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	for _, id := range original {
		if seen[id] != 1 {
			t.Fatalf("original row %s seen %d times", id, seen[id])
		}
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("%s repeated", id)
		}
	}
	if !sort.StringsAreSorted(got) {
		t.Fatal("pages are not in contract order")
	}
	var order []string
	for _, id := range got {
		for _, o := range original {
			if id == o {
				order = append(order, id)
			}
		}
	}
	if strings.Join(order, ",") != strings.Join(original, ",") {
		t.Fatal("original rows skipped or reordered")
	}
}

func TestStaleAndTamperedCursorsGetClearErrors(t *testing.T) {
	s := catalogServer(t)
	_, cursor := pageOf(t, s, "limit=1")
	if cursor == "" {
		t.Fatal("fixture should have a second page")
	}
	if ids, _ := pageOf(t, s, "limit=1&cursor="+url.QueryEscape(cursor)); len(ids) != 1 {
		t.Fatal("a fresh cursor must continue")
	}
	// The same cursor under a different ruleset version is from another version.
	s.options.Rulesets = map[int]string{41: "sep41-next"}
	code, kind, msg := errorBody(t, s, "/v1/contracts?limit=1&cursor="+url.QueryEscape(cursor))
	if code != 400 || kind != "bad_request" || !strings.Contains(msg, "restart paging") {
		t.Fatalf("%d %s %s", code, kind, msg)
	}
	// A version 1 cursor (before versioning) is refused the same way.
	v1 := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"id":"` + catalogID(t, 1) + `"}`))
	if code, _, msg := errorBody(t, s, "/v1/contracts?limit=1&cursor="+url.QueryEscape(v1)); code != 400 || !strings.Contains(msg, "restart paging") {
		t.Fatalf("%d %s", code, msg)
	}
	// Tampered or garbage cursors say only "invalid cursor", on every paged route.
	flipped := cursor[:len(cursor)-2] + "AA"
	for _, p := range []string{
		"/v1/contracts?cursor=" + url.QueryEscape(flipped),
		"/v1/contracts?cursor=bad",
		"/v1/wasm/" + strings.Repeat("0", 63) + "1?cursor=" + url.QueryEscape(flipped),
	} {
		if code, _, msg := errorBody(t, s, p); code != 400 || msg != "invalid cursor" {
			t.Fatalf("%s: %d %q", p, code, msg)
		}
	}
	if _, _, msg := errorBody(t, s, "/v1/contracts?limit=0"); msg != "limit must be 1..500" {
		t.Fatalf("filter errors must keep their own message: %q", msg)
	}
}
