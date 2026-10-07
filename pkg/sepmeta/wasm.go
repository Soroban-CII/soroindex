package sepmeta

import (
	"bytes"
	"errors"
	"fmt"
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
func ReadCustomSections(wasm []byte, names []string, lim Limits) (map[string][]byte, error) {
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
