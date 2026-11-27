package sepmeta

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// Sentinel errors. Callers match them with errors.Is; the wrapped message
// carries the offset or limit name for humans.
var (
	// ErrNotWasm means the input does not start with the Wasm magic and
	// version 1, so it is not a module this package can read.
	ErrNotWasm = errors.New("not a wasm v1 module")
	// ErrTruncated means a length or payload ran past the end of the input.
	ErrTruncated = errors.New("truncated input")
	// ErrMalformed means the bytes are structurally invalid in a way that is
	// not truncation, such as an over-long LEB128 or a non-UTF-8 name.
	ErrMalformed = errors.New("malformed input")
	// ErrLimit means the input exceeded one of the configured Limits. The
	// error is a *LimitError naming which one.
	ErrLimit = errors.New("limit exceeded")
)

// Limits caps the resources one untrusted input may consume. They exist so
// that a crafted Wasm cannot make the indexer allocate or loop without bound.
// A zero field means "use the DefaultLimits value", never "unlimited".
type Limits struct {
	// MaxWasmBytes caps the total size of a Wasm module.
	MaxWasmBytes int
	// MaxSectionBytes caps the payload of each requested custom section,
	// after concatenating repeated sections of the same name.
	MaxSectionBytes int
	// MaxSections caps how many sections of any kind a module may have.
	MaxSections int
	// MaxXDRDepth caps nesting depth when decoding XDR from a section.
	MaxXDRDepth uint
}

// DefaultLimits returns the limits from CLAUDE.md §5.1. They are well above
// what the Soroban network accepts for contract code, so real contracts never
// hit them.
func DefaultLimits() Limits {
	return Limits{
		MaxWasmBytes:    1 << 20,   // 1 MiB
		MaxSectionBytes: 256 << 10, // 256 KiB
		MaxSections:     10_000,
		MaxXDRDepth:     500,
	}
}

// Hard upper bounds on a configured Limits. They are not defaults: they are
// the point past which a larger value buys nothing, so that a typo or a
// hostile configuration file cannot turn the parser into an unbounded
// allocator. Validate enforces them, and an operator-facing caller
// (internal/config) applies that check before any Wasm is read.
const (
	// MaxConfigurableWasmBytes caps MaxWasmBytes at 64 MiB. A module reaches
	// this package only inside one CONTRACT_CODE ledger entry that the RPC
	// client delivered as base64 in a single response body, and that body is
	// itself capped (internal/rpc DefaultMaxResponseBytes, 256 MiB). The
	// largest module in the committed mainnet census is 130,278 bytes
	// (report/mainnet-raw.csv, 7 October 2026), so 64 MiB is already ~500x
	// the real maximum: it leaves room for a network that raises its
	// contract-size setting while keeping one analysis's allocation bounded.
	MaxConfigurableWasmBytes = 64 << 20
	// MaxConfigurableXDRDepth caps MaxXDRDepth at 1500, the value of
	// DecodeDefaultMaxDepth in github.com/stellar/go-xdr/xdr3
	// (v0.0.0-20260806060815-dc590f17552a, the version go-stellar-sdk pins),
	// which the SDK decoder applies when a caller passes no depth. A deeper
	// setting only grows the recursion a crafted section can drive; it
	// cannot make the SDK accept XDR it would otherwise reject.
	MaxConfigurableXDRDepth = 1500
	// minSectionEncodedBytes is the smallest a Wasm section can be on the
	// wire: a one-byte section id plus a one-byte LEB128 payload size. It
	// turns MaxWasmBytes into an upper bound on MaxSections, because a
	// module of n bytes cannot hold more than n/2 sections.
	minSectionEncodedBytes = 2
)

// ErrInvalidLimit means a configured limit is out of range: below one, or
// above a bound past which it could never be reached. It is reported at
// startup, never mid-parse, so an operator learns of a bad value before any
// untrusted byte is read.
var ErrInvalidLimit = errors.New("invalid limit")

// LimitRangeError says which Limits field is out of range and why. It
// matches ErrInvalidLimit under errors.Is. Limit is the struct field name so
// that a caller which owns flags can map it to its own flag name. Got is the
// rejected value as text, because the fields it reports are of different
// integer types and the value is only ever shown to a human.
type LimitRangeError struct {
	Limit  string // field name in Limits, e.g. "MaxSections"
	Got    string
	Reason string // human-readable bound, e.g. "at most 1500"
}

func (e *LimitRangeError) Error() string {
	return fmt.Sprintf("%s: %s is %s, want %s", ErrInvalidLimit, e.Limit, e.Got, e.Reason)
}

// Is makes errors.Is(err, ErrInvalidLimit) true for any *LimitRangeError.
func (e *LimitRangeError) Is(target error) bool { return target == ErrInvalidLimit }

// Validate reports whether lim is a usable configuration. Unlike
// withDefaults, it treats zero as an error rather than "use the default",
// because a zero that reached an operator's flag is a mistake, not a
// request. Every bound is justified at its constant above.
func (lim Limits) Validate() error {
	switch {
	case lim.MaxWasmBytes < 1:
		return &LimitRangeError{Limit: "MaxWasmBytes", Got: strconv.Itoa(lim.MaxWasmBytes), Reason: "at least 1"}
	case lim.MaxWasmBytes > MaxConfigurableWasmBytes:
		return &LimitRangeError{Limit: "MaxWasmBytes", Got: strconv.Itoa(lim.MaxWasmBytes),
			Reason: fmt.Sprintf("at most %d bytes, the hard cap", MaxConfigurableWasmBytes)}
	case lim.MaxSectionBytes < 1:
		return &LimitRangeError{Limit: "MaxSectionBytes", Got: strconv.Itoa(lim.MaxSectionBytes), Reason: "at least 1"}
	case lim.MaxSectionBytes > lim.MaxWasmBytes:
		return &LimitRangeError{Limit: "MaxSectionBytes", Got: strconv.Itoa(lim.MaxSectionBytes),
			Reason: fmt.Sprintf("at most MaxWasmBytes (%d), because a section is part of the module that holds it", lim.MaxWasmBytes)}
	case lim.MaxSections < 1:
		return &LimitRangeError{Limit: "MaxSections", Got: strconv.Itoa(lim.MaxSections), Reason: "at least 1"}
	case lim.MaxSections > lim.MaxWasmBytes/minSectionEncodedBytes:
		return &LimitRangeError{Limit: "MaxSections", Got: strconv.Itoa(lim.MaxSections),
			Reason: fmt.Sprintf("at most %d, the most sections a %d-byte module can hold (each costs at least %d bytes on the wire)",
				lim.MaxWasmBytes/minSectionEncodedBytes, lim.MaxWasmBytes, minSectionEncodedBytes)}
	case lim.MaxXDRDepth < 1:
		return &LimitRangeError{Limit: "MaxXDRDepth", Got: strconv.FormatUint(uint64(lim.MaxXDRDepth), 10), Reason: "at least 1"}
	case lim.MaxXDRDepth > MaxConfigurableXDRDepth:
		return &LimitRangeError{Limit: "MaxXDRDepth", Got: strconv.FormatUint(uint64(lim.MaxXDRDepth), 10),
			Reason: fmt.Sprintf("at most %d, the XDR decoder's own maximum nesting depth", MaxConfigurableXDRDepth)}
	}
	return nil
}

// withDefaults returns lim with every zero field replaced by its default, so
// a zero Limits{} is safe rather than "allow nothing" or "allow anything".
func (lim Limits) withDefaults() Limits {
	d := DefaultLimits()
	if lim.MaxWasmBytes <= 0 {
		lim.MaxWasmBytes = d.MaxWasmBytes
	}
	if lim.MaxSectionBytes <= 0 {
		lim.MaxSectionBytes = d.MaxSectionBytes
	}
	if lim.MaxSections <= 0 {
		lim.MaxSections = d.MaxSections
	}
	if lim.MaxXDRDepth == 0 {
		lim.MaxXDRDepth = d.MaxXDRDepth
	}
	return lim
}

// LimitError reports which limit was exceeded. It matches ErrLimit under
// errors.Is so callers need not type-assert.
type LimitError struct {
	Limit string // field name in Limits, e.g. "MaxSectionBytes"
	Max   int
	Got   int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("%s: %s is %d, got %d", ErrLimit, e.Limit, e.Max, e.Got)
}

// Is makes errors.Is(err, ErrLimit) true for any *LimitError.
func (e *LimitError) Is(target error) bool { return target == ErrLimit }

var wasmHeader = []byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00}

const customSectionID = 0

// ReadCustomSections returns the payloads of the custom sections whose names
// are in names. Sections that share a name are concatenated in file order,
// because the Soroban toolchain emits one contractmetav0 section per
// contractmeta! invocation. Names that do not occur are absent from the map.
//
// Non-custom sections, including code, are skipped without being decoded.
//
// Zero fields in lim take their DefaultLimits value.
func ReadCustomSections(wasm []byte, names []string, lim Limits) (map[string][]byte, error) {
	lim = lim.withDefaults()
	if len(wasm) > lim.MaxWasmBytes {
		return nil, &LimitError{Limit: "MaxWasmBytes", Max: lim.MaxWasmBytes, Got: len(wasm)}
	}
	if !bytes.HasPrefix(wasm, wasmHeader) {
		return nil, ErrNotWasm
	}

	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	out := make(map[string][]byte)

	r := reader{buf: wasm, off: len(wasmHeader)}
	for count := 0; r.remaining() > 0; count++ {
		if count >= lim.MaxSections {
			return nil, &LimitError{Limit: "MaxSections", Max: lim.MaxSections, Got: count + 1}
		}
		start := r.off
		id, err := r.byte()
		if err != nil {
			return nil, fmt.Errorf("section at offset %d: %w", start, err)
		}
		payload, err := r.sized()
		if err != nil {
			return nil, fmt.Errorf("section id %d at offset %d: %w", id, start, err)
		}
		if id != customSectionID {
			continue
		}

		pr := reader{buf: payload}
		nameBytes, err := pr.sized()
		if err != nil {
			return nil, fmt.Errorf("custom section name at offset %d: %w", start, err)
		}
		if !utf8.Valid(nameBytes) {
			return nil, fmt.Errorf("custom section name at offset %d: %w: name is not UTF-8", start, ErrMalformed)
		}
		if !want[string(nameBytes)] {
			continue
		}
		name := string(nameBytes)
		body := payload[pr.off:]
		total := len(out[name]) + len(body)
		if total > lim.MaxSectionBytes {
			return nil, &LimitError{Limit: "MaxSectionBytes", Max: lim.MaxSectionBytes, Got: total}
		}
		out[name] = append(out[name], body...)
	}
	return out, nil
}

// reader walks a byte slice. Every read is bounds-checked against the bytes
// remaining before it slices or allocates.
type reader struct {
	buf []byte
	off int
}

func (r *reader) remaining() int { return len(r.buf) - r.off }

func (r *reader) byte() (byte, error) {
	if r.remaining() < 1 {
		return 0, ErrTruncated
	}
	b := r.buf[r.off]
	r.off++
	return b, nil
}

// u32 reads an unsigned LEB128 value of at most 5 bytes, rejecting encodings
// whose fifth byte sets bits beyond 32 or a continuation bit.
func (r *reader) u32() (uint32, error) {
	var v uint32
	for i := 0; i < 5; i++ {
		b, err := r.byte()
		if err != nil {
			return 0, err
		}
		if i == 4 && b&0xf0 != 0 {
			return 0, fmt.Errorf("%w: LEB128 u32 exceeds 32 bits", ErrMalformed)
		}
		v |= uint32(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return v, nil
		}
	}
	return 0, fmt.Errorf("%w: LEB128 u32 longer than 5 bytes", ErrMalformed) // unreachable: the i==4 check rejects a continuation bit
}

// sized reads a LEB128 length then returns that many bytes as a subslice of
// the input, without copying. The length is checked against the bytes
// remaining first, so a huge declared size cannot cause an allocation.
func (r *reader) sized() ([]byte, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	if int64(n) > int64(r.remaining()) {
		return nil, fmt.Errorf("%w: size %d exceeds %d bytes remaining", ErrTruncated, n, r.remaining())
	}
	b := r.buf[r.off : r.off+int(n)]
	r.off += int(n)
	return b, nil
}
