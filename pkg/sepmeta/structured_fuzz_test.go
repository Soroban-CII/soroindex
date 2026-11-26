package sepmeta

import (
	"bytes"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Structure-aware fuzzing. Byte-level mutation of a contractspecv0 payload
// almost always breaks the XDR in its first few words, so FuzzDecodeSpec
// rarely reaches nested types such as Map<K,Vec<Option<udt:T>>>. The targets
// here instead read the fuzzer's bytes as a list of choices (how many
// entries, which kind, how deep, how long each string) and build a stream
// that is valid by construction. The decoders must then either return it
// exactly or report a limit.
//
// go test -fuzz only passes primitive arguments, so the structure is derived
// from one []byte by choices, a small deterministic decoder: the same bytes
// always build the same stream, and a crash file replays exactly.

// choices hands out decisions from fuzzer bytes. When the bytes run out every
// decision is 0, so any input, including an empty one, builds a stream.
type choices struct {
	b []byte
	i int
}

func (c *choices) byte() byte {
	if c.i >= len(c.b) {
		return 0
	}
	v := c.b[c.i]
	c.i++
	return v
}

// intn returns a value in [0, n). n must be in [1, 65536].
func (c *choices) intn(n int) int {
	if n <= 1 {
		return 0
	}
	v := int(c.byte())
	if n > 256 {
		v = v<<8 | int(c.byte())
	}
	return v % n
}

func (c *choices) bool() bool { return c.byte()&1 == 1 }

// u32 is intn for XDR uint32 fields.
func (c *choices) u32(n int) uint32 {
	return uint32(c.intn(n)) // #nosec G115 -- intn returns [0, n) with n <= 65536
}

// i32 is intn for XDR enum fields.
func (c *choices) i32(n int) int32 {
	return int32(c.intn(n)) // #nosec G115 -- intn returns [0, n) with n <= 65536
}

// str returns a string of at most maxLen bytes. Most strings are short; one
// choice in eight may use the full length, so streams can cross a size limit.
// Bytes are arbitrary: XDR strings carry no encoding.
func (c *choices) str(maxLen int) string {
	n := c.intn(min(maxLen, 16) + 1)
	if c.byte()%8 == 0 {
		n = c.intn(maxLen + 1)
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = c.byte()
	}
	return string(out)
}

// fuzzLimits picks the limits a generated stream is decoded under: usually
// small ones, so the generator can cross them, sometimes the defaults.
func (c *choices) fuzzLimits() Limits {
	if c.byte()%4 == 0 {
		return DefaultLimits()
	}
	return Limits{
		MaxWasmBytes:    1 << 20,
		MaxSectionBytes: 64 + c.intn(8192),
		MaxSections:     10_000,
		MaxXDRDepth:     uint(1 + c.intn(48)),
	}
}

// genScalars is the generator's own table of scalar types and their expected
// names. It deliberately does not reuse scalarNames: it is the oracle that
// TypeName is checked against.
var genScalars = []struct {
	t    xdr.ScSpecType
	name string
}{
	{xdr.ScSpecTypeScSpecTypeVal, "Val"}, {xdr.ScSpecTypeScSpecTypeBool, "bool"},
	{xdr.ScSpecTypeScSpecTypeVoid, "void"}, {xdr.ScSpecTypeScSpecTypeError, "Error"},
	{xdr.ScSpecTypeScSpecTypeU32, "u32"}, {xdr.ScSpecTypeScSpecTypeI32, "i32"},
	{xdr.ScSpecTypeScSpecTypeU64, "u64"}, {xdr.ScSpecTypeScSpecTypeI64, "i64"},
	{xdr.ScSpecTypeScSpecTypeTimepoint, "Timepoint"}, {xdr.ScSpecTypeScSpecTypeDuration, "Duration"},
	{xdr.ScSpecTypeScSpecTypeU128, "u128"}, {xdr.ScSpecTypeScSpecTypeI128, "i128"},
	{xdr.ScSpecTypeScSpecTypeU256, "u256"}, {xdr.ScSpecTypeScSpecTypeI256, "i256"},
	{xdr.ScSpecTypeScSpecTypeBytes, "Bytes"}, {xdr.ScSpecTypeScSpecTypeString, "String"},
	{xdr.ScSpecTypeScSpecTypeSymbol, "Symbol"}, {xdr.ScSpecTypeScSpecTypeAddress, "Address"},
	{xdr.ScSpecTypeScSpecTypeMuxedAddress, "MuxedAddress"},
}

// specGen builds spec entries. maxNest bounds type nesting; nest records the
// deepest nesting actually built.
type specGen struct {
	c       *choices
	maxNest int
	nest    int
}

// typ builds a type at nesting level depth and returns it with the name
// Functions must render for it.
func (g *specGen) typ(depth int) (xdr.ScSpecTypeDef, string) {
	g.nest = max(g.nest, depth)
	k := g.c.intn(len(genScalars) + 8)
	if depth >= g.maxNest {
		k %= len(genScalars)
	}
	if k < len(genScalars) {
		s := genScalars[k]
		return xdr.ScSpecTypeDef{Type: s.t}, s.name
	}
	switch k - len(genScalars) {
	case 0:
		v, n := g.typ(depth + 1)
		return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeOption, Option: &xdr.ScSpecTypeOption{ValueType: v}}, "Option<" + n + ">"
	case 1:
		ok, okn := g.typ(depth + 1)
		er, ern := g.typ(depth + 1)
		return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeResult, Result: &xdr.ScSpecTypeResult{OkType: ok, ErrorType: er}}, "Result<" + okn + "," + ern + ">"
	case 2:
		v, n := g.typ(depth + 1)
		return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeVec, Vec: &xdr.ScSpecTypeVec{ElementType: v}}, "Vec<" + n + ">"
	case 3:
		key, kn := g.typ(depth + 1)
		val, vn := g.typ(depth + 1)
		return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeMap, Map: &xdr.ScSpecTypeMap{KeyType: key, ValueType: val}}, "Map<" + kn + "," + vn + ">"
	case 4:
		n := g.c.intn(13) // the XDR allows at most 12
		vals := make([]xdr.ScSpecTypeDef, n)
		names := make([]string, n)
		for i := range vals {
			vals[i], names[i] = g.typ(depth + 1)
		}
		return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeTuple, Tuple: &xdr.ScSpecTypeTuple{ValueTypes: vals}}, "(" + strings.Join(names, ",") + ")"
	case 5:
		n := g.c.u32(65536)
		return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeBytesN, BytesN: &xdr.ScSpecTypeBytesN{N: xdr.Uint32(n)}}, "BytesN<" + strconv.FormatUint(uint64(n), 10) + ">"
	default:
		name := g.c.str(60)
		return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeUdt, Udt: &xdr.ScSpecTypeUdt{Name: name}}, "udt:" + name
	}
}

// entries builds a spec stream and the FnSig list Functions must return for
// it: one per function entry, in order.
func (g *specGen) entries() ([]xdr.ScSpecEntry, []FnSig) {
	var out []xdr.ScSpecEntry
	var fns []FnSig
	for n := g.c.intn(9); n > 0; n-- {
		switch g.c.intn(6) {
		case 0, 1: // functions are what the matcher reads, so they come up most
			f := xdr.ScSpecFunctionV0{Doc: g.c.str(1024), Name: xdr.ScSymbol(g.c.str(32))}
			sig := FnSig{Name: string(f.Name), Inputs: []string{}, Outputs: []string{}}
			for i := g.c.intn(11); i > 0; i-- {
				t, name := g.typ(0)
				f.Inputs = append(f.Inputs, xdr.ScSpecFunctionInputV0{Doc: g.c.str(1024), Name: g.c.str(30), Type: t})
				sig.Inputs = append(sig.Inputs, name)
			}
			if g.c.bool() {
				t, name := g.typ(0)
				f.Outputs = []xdr.ScSpecTypeDef{t}
				sig.Outputs = append(sig.Outputs, name)
			}
			out = append(out, xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0, FunctionV0: &f})
			fns = append(fns, sig)
		case 2:
			s := xdr.ScSpecUdtStructV0{Doc: g.c.str(1024), Lib: g.c.str(80), Name: g.c.str(60)}
			for i := g.c.intn(6); i > 0; i-- {
				t, _ := g.typ(0)
				s.Fields = append(s.Fields, xdr.ScSpecUdtStructFieldV0{Doc: g.c.str(1024), Name: g.c.str(30), Type: t})
			}
			out = append(out, xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryUdtStructV0, UdtStructV0: &s})
		case 3:
			u := xdr.ScSpecUdtUnionV0{Doc: g.c.str(1024), Lib: g.c.str(80), Name: g.c.str(60)}
			for i := g.c.intn(6); i > 0; i-- {
				if g.c.bool() {
					u.Cases = append(u.Cases, xdr.ScSpecUdtUnionCaseV0{Kind: xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseVoidV0,
						VoidCase: &xdr.ScSpecUdtUnionCaseVoidV0{Doc: g.c.str(1024), Name: g.c.str(60)}})
					continue
				}
				tc := xdr.ScSpecUdtUnionCaseTupleV0{Doc: g.c.str(1024), Name: g.c.str(60)}
				for j := g.c.intn(4); j > 0; j-- {
					t, _ := g.typ(0)
					tc.Type = append(tc.Type, t)
				}
				u.Cases = append(u.Cases, xdr.ScSpecUdtUnionCaseV0{Kind: xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseTupleV0, TupleCase: &tc})
			}
			out = append(out, xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryUdtUnionV0, UdtUnionV0: &u})
		case 4:
			if g.c.bool() {
				e := xdr.ScSpecUdtEnumV0{Doc: g.c.str(1024), Lib: g.c.str(80), Name: g.c.str(60)}
				for i := g.c.intn(6); i > 0; i-- {
					e.Cases = append(e.Cases, xdr.ScSpecUdtEnumCaseV0{Doc: g.c.str(1024), Name: g.c.str(60), Value: xdr.Uint32(g.c.u32(65536))})
				}
				out = append(out, xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryUdtEnumV0, UdtEnumV0: &e})
				continue
			}
			e := xdr.ScSpecUdtErrorEnumV0{Doc: g.c.str(1024), Lib: g.c.str(80), Name: g.c.str(60)}
			for i := g.c.intn(6); i > 0; i-- {
				e.Cases = append(e.Cases, xdr.ScSpecUdtErrorEnumCaseV0{Doc: g.c.str(1024), Name: g.c.str(60), Value: xdr.Uint32(g.c.u32(65536))})
			}
			out = append(out, xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryUdtErrorEnumV0, UdtErrorEnumV0: &e})
		default:
			ev := xdr.ScSpecEventV0{Doc: g.c.str(1024), Lib: g.c.str(80), Name: g.c.str(60),
				DataFormat: xdr.ScSpecEventDataFormat(g.c.i32(3))}
			for i := g.c.intn(3); i > 0; i-- {
				ev.PrefixTopics = append(ev.PrefixTopics, xdr.ScSymbol(g.c.str(32)))
			}
			for i := g.c.intn(5); i > 0; i-- {
				t, _ := g.typ(0)
				ev.Params = append(ev.Params, xdr.ScSpecEventParamV0{Doc: g.c.str(1024), Name: g.c.str(30), Type: t,
					Location: xdr.ScSpecEventParamLocationV0(g.c.i32(2))})
			}
			out = append(out, xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryEventV0, EventV0: &ev})
		}
	}
	return out, fns
}

// encodeStream marshals values back to back, as a custom section holds them.
func encodeStream[T any](t *testing.T, vals []T) ([]byte, []int) {
	t.Helper()
	var buf bytes.Buffer
	ends := make([]int, 0, len(vals)) // byte offset where each value ends
	for _, v := range vals {
		if _, err := xdr.Marshal(&buf, v); err != nil {
			t.Fatalf("generator built a value the encoder rejects: %v", err)
		}
		ends = append(ends, buf.Len())
	}
	return buf.Bytes(), ends
}

// checkTruncation cuts a valid stream at an input-chosen offset inside it.
// The entries wholly before the cut must come back unchanged, and the cut
// must be ErrTruncated: the bytes are a prefix of a valid stream, never
// malformed.
func checkTruncation[T any](t *testing.T, c *choices, stream []byte, ends []int, decode func([]byte) ([]T, error), same func(a, b []T) bool, want []T) {
	t.Helper()
	if len(stream) == 0 {
		return
	}
	cut := c.intn(min(len(stream), 65536))
	got, err := decode(stream[:cut])
	whole := 0
	for whole < len(ends) && ends[whole] <= cut {
		whole++
	}
	atBoundary := cut == 0 || (whole > 0 && ends[whole-1] == cut)
	switch {
	case atBoundary && err != nil:
		t.Fatalf("cut at entry boundary %d: unexpected error %v", cut, err)
	case !atBoundary && !errors.Is(err, ErrTruncated):
		t.Fatalf("cut at %d inside entry %d: err = %v, want ErrTruncated", cut, whole, err)
	}
	if len(got) != whole || !same(got, want[:whole]) {
		t.Fatalf("cut at %d: decoded %d entries, want the %d before the cut", cut, len(got), whole)
	}
}

// sameEncoding compares spec entries by their XDR encoding, so a nil and an
// empty slice inside an entry count as equal, as they do on the wire.
func sameEncoding(t *testing.T, a, b []xdr.ScSpecEntry) bool {
	t.Helper()
	ea, _ := encodeStream(t, a)
	eb, _ := encodeStream(t, b)
	return len(a) == len(b) && bytes.Equal(ea, eb)
}

// decodedOrLimit checks the outcome of decoding a valid stream: success, or
// an ErrLimit that the stream really does exceed.
func decodedOrLimit(t *testing.T, err error, stream []byte, lim Limits) (limited bool) {
	t.Helper()
	if err == nil {
		return false
	}
	var le *LimitError
	if !errors.As(err, &le) {
		t.Fatalf("valid %d-byte stream: err = %v, want nil or ErrLimit", len(stream), err)
	}
	switch le.Limit {
	case "MaxSectionBytes":
		if len(stream) <= lim.MaxSectionBytes {
			t.Fatalf("MaxSectionBytes error for %d bytes under limit %d", len(stream), lim.MaxSectionBytes)
		}
	case "MaxXDRDepth":
		if lim.MaxXDRDepth >= DefaultLimits().MaxXDRDepth {
			t.Fatalf("MaxXDRDepth error at the default limit; the generator nests at most %d", maxGenNest)
		}
	default:
		t.Fatalf("unexpected limit %s", le.Limit)
	}
	return true
}

// maxGenNest bounds type nesting. The SDK counts several decoder levels per
// type level, and 24 type levels stay well inside the default MaxXDRDepth of
// 500, so at the default every generated stream must decode.
const maxGenNest = 24

func structuredSeeds(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 3, 1, 1, 0})
	f.Add(bytes.Repeat([]byte{0x1a}, 64))                // deep Option nesting
	f.Add(bytes.Repeat([]byte{0x1d, 0x1e, 0x13}, 80))    // Map / Tuple mixes
	f.Add(bytes.Repeat([]byte{0xff, 0x08, 0x40, 1}, 64)) // long strings, many entries
	f.Add([]byte("structure-aware seed: functions with nested Map<K,Vec<Option<udt>>>"))
}

func FuzzDecodeSpecStructured(f *testing.F) {
	structuredSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		c := &choices{b: data}
		lim := c.fuzzLimits()
		g := &specGen{c: c, maxNest: c.intn(maxGenNest + 1)}
		entries, wantFns := g.entries()
		stream, ends := encodeStream(t, entries)

		got, err := DecodeSpec(stream, lim)
		if decodedOrLimit(t, err, stream, lim) {
			// Whatever decoded before the limit must be a prefix of the input.
			if len(got) > len(entries) || !sameEncoding(t, got, entries[:len(got)]) {
				t.Fatalf("decoded %d entries before the limit that differ from the input", len(got))
			}
			// The same stream under the default limits must decode.
			if lim.MaxSectionBytes >= len(stream) {
				if _, err := DecodeSpec(stream, Limits{MaxSectionBytes: lim.MaxSectionBytes}); err != nil {
					t.Fatalf("stream nested %d deep fails at the default depth: %v", g.nest, err)
				}
			}
			return
		}
		// Decode then re-encode must reproduce the stream byte for byte.
		again, _ := encodeStream(t, got)
		if !bytes.Equal(again, stream) {
			t.Fatalf("re-encoding %d decoded entries changed %d bytes to %d", len(got), len(stream), len(again))
		}
		// Functions must render every type exactly as the generator expects.
		fns := Functions(got)
		if len(fns) != len(wantFns) || (len(fns) > 0 && !reflect.DeepEqual(fns, wantFns)) {
			t.Fatalf("Functions = %+v\nwant %+v", fns, wantFns)
		}
		checkTruncation(t, c, stream, ends, func(b []byte) ([]xdr.ScSpecEntry, error) { return DecodeSpec(b, lim) },
			func(a, b []xdr.ScSpecEntry) bool { return sameEncoding(t, a, b) }, entries)
	})
}

func FuzzDecodeMetaStructured(f *testing.F) {
	structuredSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		c := &choices{b: data}
		lim := c.fuzzLimits()
		var raw []xdr.ScMetaEntry
		var want []MetaEntry
		for n := c.intn(17); n > 0; n-- {
			key := c.str(64)
			if c.byte()%3 == 0 {
				key = SEPMetaKey // make sep entries common so ParseSEP47 sees them
			}
			val := c.str(6000) // can exceed ParseSEP47's 4 KiB value cap
			raw = append(raw, xdr.ScMetaEntry{Kind: xdr.ScMetaKindScMetaV0, V0: &xdr.ScMetaV0{Key: key, Val: val}})
			want = append(want, MetaEntry{Key: key, Value: val})
		}
		stream, ends := encodeStream(t, raw)

		got, err := DecodeMeta(stream, lim)
		if decodedOrLimit(t, err, stream, lim) {
			if len(got) != 0 {
				t.Fatalf("section over the size limit decoded %d entries", len(got))
			}
			return
		}
		if len(got) != len(want) || (len(got) > 0 && !reflect.DeepEqual(got, want)) {
			t.Fatalf("DecodeMeta = %q, want %q", got, want)
		}
		again, _ := encodeStream(t, raw[:0:0])
		for _, e := range got {
			b, _ := encodeStream(t, []xdr.ScMetaEntry{{Kind: xdr.ScMetaKindScMetaV0, V0: &xdr.ScMetaV0{Key: e.Key, Val: e.Value}}})
			again = append(again, b...)
		}
		if !bytes.Equal(again, stream) {
			t.Fatalf("re-encoding changed the stream")
		}
		// ParseSEP47 must count every sep entry, whatever its value.
		sepCount := 0
		for _, e := range want {
			if e.Key == SEPMetaKey {
				sepCount++
			}
		}
		for _, mode := range []Mode{Lenient, Strict} {
			if c := ParseSEP47(got, mode); c.EntryCount != sepCount {
				t.Fatalf("EntryCount = %d, want %d", c.EntryCount, sepCount)
			}
		}
		checkTruncation(t, c, stream, ends, func(b []byte) ([]MetaEntry, error) { return DecodeMeta(b, lim) },
			func(a, b []MetaEntry) bool { return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b)) }, want)
	})
}
