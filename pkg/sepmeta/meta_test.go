package sepmeta

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// encodeMeta encodes entries as a contractmetav0 payload.
func encodeMeta(t testing.TB, entries ...MetaEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, e := range entries {
		v := xdr.ScMetaEntry{Kind: xdr.ScMetaKindScMetaV0, V0: &xdr.ScMetaV0{Key: e.Key, Val: e.Value}}
		if _, err := xdr.Marshal(&buf, v); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// toolchainMeta is the meta every fixture gets from soroban-sdk 28.0.0 and
// stellar-cli 28.0.0 (testdata/contracts/README.md).
var toolchainMeta = []MetaEntry{
	{"rsver", "1.98.1"},
	{"rssdkver", "28.0.0#48d506712f964094d14176e2f0b02afcd1054567"},
	{"rssdk_spec_shaking", "2"},
	{"cliver", "28.0.0#300aaf69ab100536678bdb641428b06f06b318ea"},
}

func TestDecodeMetaFixtures(t *testing.T) {
	tests := []struct {
		fixture string
		want    []MetaEntry
	}{
		{"token_full_sep.wasm", append([]MetaEntry{{"sep", "41"}}, toolchainMeta...)},
		{"token_partial.wasm", append([]MetaEntry{{"sep", "41"}}, toolchainMeta...)},
		{"not_token.wasm", append([]MetaEntry{{"sep", "41"}}, toolchainMeta...)},
		{"multi_sep.wasm", append([]MetaEntry{{"sep", "41"}, {"sep", "40"}}, toolchainMeta...)},
		{"token_full_legacy.wasm", toolchainMeta},
	}
	for _, tt := range tests {
		t.Run(tt.fixture+" decodes every entry in order", func(t *testing.T) {
			got, err := DecodeMeta(fixtureSection(t, tt.fixture, SectionMeta), DefaultLimits())
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %v\nwant %v", got, tt.want)
			}
		})
	}
}

func TestMetaAbsentFromDerivedFixtures(t *testing.T) {
	t.Run("no_meta.wasm has a spec section but no meta section", func(t *testing.T) {
		secs, err := ReadCustomSections(fixture(t, "no_meta.wasm"), []string{SectionMeta, SectionSpec}, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := secs[SectionMeta]; ok {
			t.Error("meta section present")
		}
		if len(secs[SectionSpec]) == 0 {
			t.Error("spec section missing")
		}
	})
	t.Run("truncated.wasm is rejected as truncated at the section level", func(t *testing.T) {
		_, err := ReadCustomSections(fixture(t, "truncated.wasm"), []string{SectionMeta}, DefaultLimits())
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("err = %v, want ErrTruncated", err)
		}
	})
}

func TestDecodeMeta(t *testing.T) {
	two := encodeMeta(t, MetaEntry{"sep", "41"}, MetaEntry{"k", "v"})
	tests := []struct {
		name    string
		in      []byte
		want    []MetaEntry
		wantErr error
	}{
		{"empty section has no entries", nil, nil, nil},
		{"two entries decode in order", two, []MetaEntry{{"sep", "41"}, {"k", "v"}}, nil},
		{"empty key and value are kept", encodeMeta(t, MetaEntry{"", ""}), []MetaEntry{{"", ""}}, nil},
		{"non-UTF-8 value is kept as raw bytes", encodeMeta(t, MetaEntry{"sep", "\xff41"}), []MetaEntry{{"sep", "\xff41"}}, nil},
		{
			"unknown meta kind is malformed, earlier entries kept",
			append(encodeMeta(t, MetaEntry{"sep", "41"}), 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0),
			[]MetaEntry{{"sep", "41"}},
			ErrMalformed,
		},
		{
			"declared string longer than any section is malformed",
			[]byte{0, 0, 0, 0, 0xff, 0xff, 0xff, 0xf0},
			[]MetaEntry{},
			ErrMalformed,
		},
		{
			"trailing three bytes are a truncated entry",
			append(append([]byte(nil), two...), 0, 0, 0),
			[]MetaEntry{{"sep", "41"}, {"k", "v"}},
			ErrTruncated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeMeta(tt.in, DefaultLimits())
			if tt.wantErr == nil && err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if len(got) != len(tt.want) || (len(got) > 0 && !reflect.DeepEqual(got, tt.want)) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDecodeMetaEveryPrefix cuts a real meta section at every byte. A cut on
// an entry boundary decodes cleanly; any other cut is ErrTruncated and
// returns exactly the entries that ended before the cut.
func TestDecodeMetaEveryPrefix(t *testing.T) {
	section := fixtureSection(t, "multi_sep.wasm", SectionMeta)
	full, err := DecodeMeta(section, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	// Entry end offsets, from re-encoding each entry.
	var ends []int
	off := 0
	for _, e := range full {
		off += len(encodeMeta(t, e))
		ends = append(ends, off)
	}
	if off != len(section) {
		t.Fatalf("re-encoded length %d != section length %d", off, len(section))
	}
	t.Logf("checking %d cuts across %d entries", len(section), len(full))
	for cut := 0; cut < len(section); cut++ {
		complete := 0
		for _, e := range ends {
			if e <= cut {
				complete++
			}
		}
		onBoundary := cut == 0 || (complete > 0 && ends[complete-1] == cut)
		got, err := DecodeMeta(section[:cut], DefaultLimits())
		if len(got) != complete {
			t.Fatalf("cut %d: got %d entries, want %d", cut, len(got), complete)
		}
		switch {
		case onBoundary && err != nil:
			t.Fatalf("cut %d on an entry boundary: unexpected err %v", cut, err)
		case !onBoundary && !errors.Is(err, ErrTruncated):
			t.Fatalf("cut %d mid-entry: err = %v, want ErrTruncated", cut, err)
		}
	}
}

func TestDecodeMetaLimits(t *testing.T) {
	t.Run("section over MaxSectionBytes is rejected", func(t *testing.T) {
		in := encodeMeta(t, MetaEntry{"k", string(make([]byte, 64))})
		_, err := DecodeMeta(in, Limits{MaxSectionBytes: 16})
		var le *LimitError
		if !errors.As(err, &le) || le.Limit != "MaxSectionBytes" {
			t.Fatalf("err = %v, want MaxSectionBytes limit", err)
		}
	})
	t.Run("zero Limits uses the defaults", func(t *testing.T) {
		got, err := DecodeMeta(encodeMeta(t, MetaEntry{"sep", "41"}), Limits{})
		if err != nil || len(got) != 1 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}
