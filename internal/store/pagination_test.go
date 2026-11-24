package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
)

func cursorTestID(t *testing.T) string {
	t.Helper()
	id, err := strkey.Encode(strkey.VersionByteContract, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func reencode(t *testing.T, c contractCursor) string {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func TestCursorRoundTripsUnderTheSameVersions(t *testing.T) {
	id := cursorTestID(t)
	rules := map[int]string{41: "sep41-v0.5.2", 40: "x"}
	got, err := decodeContractCursor(encodeContractCursor(id, rules), map[int]string{40: "x", 41: "sep41-v0.5.2"})
	if err != nil || got != id {
		t.Fatalf("%q %v", got, err)
	}
}

func TestCursorFromAnotherVersionIsStale(t *testing.T) {
	id := cursorTestID(t)
	rules := map[int]string{41: "a"}
	schema, err := SchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	withSum := func(c contractCursor) string { c.Sum = c.checksum(); return reencode(t, c) }
	for name, cursor := range map[string]string{
		"version 1 format":         reencode(t, contractCursor{Version: 1, ID: id}),
		"another ruleset version":  encodeContractCursor(id, map[int]string{41: "b"}),
		"an added ruleset":         encodeContractCursor(id, map[int]string{41: "a", 50: "c"}),
		"another schema (re-sign)": withSum(contractCursor{Version: cursorVersion, Schema: schema + 1, Rules: rulesetStamp(rules), ID: id}),
	} {
		t.Run(name+" is stale", func(t *testing.T) {
			if _, err := decodeContractCursor(cursor, rules); !errors.Is(err, ErrStaleCursor) || !errors.Is(err, ErrBadQuery) {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestTamperedOrMalformedCursorsAreInvalidWithoutDetail(t *testing.T) {
	id := cursorTestID(t)
	rules := map[int]string{41: "a"}
	good := contractCursor{Version: cursorVersion, ID: id}
	schema, _ := SchemaVersion()
	good.Schema, good.Rules = schema, rulesetStamp(rules)
	good.Sum = good.checksum()
	edit := func(f func(*contractCursor)) string { c := good; f(&c); return reencode(t, c) }
	for name, cursor := range map[string]string{
		"not base64":            "!!!",
		"not json":              base64.RawURLEncoding.EncodeToString([]byte("plain")),
		"edited id":             edit(func(c *contractCursor) { c.ID = strings.Replace(id, id[len(id)-1:], "A", 1) }),
		"edited schema":         edit(func(c *contractCursor) { c.Schema++ }),
		"edited ruleset stamp":  edit(func(c *contractCursor) { c.Rules = "00000000" }),
		"missing checksum":      edit(func(c *contractCursor) { c.Sum = "" }),
		"wrong checksum":        edit(func(c *contractCursor) { c.Sum = "0000000000000000" }),
		"unknown future format": edit(func(c *contractCursor) { c.Version = 9; c.Sum = c.checksum() }),
		"invalid contract id":   edit(func(c *contractCursor) { c.ID = "C1"; c.Sum = c.checksum() }),
		"too long":              strings.Repeat("a", 257),
	} {
		t.Run(name+" is invalid", func(t *testing.T) {
			_, err := decodeContractCursor(cursor, rules)
			if !errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrStaleCursor) {
				t.Fatalf("%v", err)
			}
			if err.Error() != ErrInvalidCursor.Error() {
				t.Fatal("error text differs by failure cause")
			}
		})
	}
}
