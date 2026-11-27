package sepmeta

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"unicode/utf8"
)

// nameChunk is the size of the fixed buffer used to validate custom section
// names that are not requested. It bounds the memory a long name can cost.
const nameChunk = 4096

// ReadCustomSectionsAt is ReadCustomSections for a module that is not in
// memory, such as a local file or a data-lake object. It reads the module
// header, each section's id and size, each custom section's name, and the
// payloads of the requested sections, and nothing else: skipped sections are
// never read. Peak memory is therefore the requested payloads plus a fixed
// few kilobytes, whatever the module's size.
//
// size is the module's length in bytes. For the same bytes and limits, the
// result and the error are the same as ReadCustomSections returns; the
// byte-slice function is the reference and a differential test and fuzz
// target hold the two together.
//
// A read that ends early (r returns io.EOF or io.ErrUnexpectedEOF before the
// bytes size promised) is ErrTruncated. Any other error from r is returned
// wrapped and matches none of the package sentinels, because it says nothing
// about the module.
func ReadCustomSectionsAt(r io.ReaderAt, size int64, names []string, lim Limits) (map[string][]byte, error) {
	lim = lim.withDefaults()
	if size < 0 {
		return nil, fmt.Errorf("%w: negative module size %d", ErrMalformed, size)
	}
	if size > int64(lim.MaxWasmBytes) {
		return nil, &LimitError{Limit: "MaxWasmBytes", Max: lim.MaxWasmBytes, Got: clampInt(size)}
	}
	// From here size fits in an int: it is at most lim.MaxWasmBytes.
	s := streamReader{r: r, size: int(size)}
	// scratch holds the module header, then each section head and name
	// length in turn. A slice passed to io.ReaderAt escapes to the heap, so
	// one buffer reused for every small read keeps it to one allocation.
	scratch := make([]byte, len(wasmHeader))

	if s.size < len(wasmHeader) {
		return nil, ErrNotWasm
	}
	hdr := scratch[:len(wasmHeader)]
	if err := s.readAt(hdr, 0); err != nil {
		return nil, err
	}
	if !bytes.Equal(hdr, wasmHeader) {
		return nil, ErrNotWasm
	}

	want := make(map[string]bool, len(names))
	maxName := 0
	for _, n := range names {
		want[n] = true
		maxName = max(maxName, len(n))
	}
	out := make(map[string][]byte)
	// nameBuf holds a candidate name: one no longer than the longest
	// requested name. Longer names cannot match and are only validated.
	nameBuf := make([]byte, maxName)
	var chunk []byte // grown on demand, for names longer than maxName

	off := len(hdr)
	for count := 0; off < s.size; count++ {
		if count >= lim.MaxSections {
			return nil, &LimitError{Limit: "MaxSections", Max: lim.MaxSections, Got: count + 1}
		}
		start := off
		// The id byte and a LEB128 size: at most 1+5 bytes, fewer at the end.
		head := scratch[:min(6, s.size-off)]
		if err := s.readAt(head, off); err != nil {
			return nil, fmt.Errorf("section at offset %d: %w", start, err)
		}
		hr := reader{buf: head}
		id, _ := hr.byte() // n >= 1 because off < s.size
		plen, err := hr.u32()
		if err != nil {
			return nil, fmt.Errorf("section id %d at offset %d: %w", id, start, err)
		}
		off += hr.off
		if rem := s.size - off; int64(plen) > int64(rem) {
			return nil, fmt.Errorf("section id %d at offset %d: %w: size %d exceeds %d bytes remaining", id, start, ErrTruncated, plen, rem)
		}
		payloadStart, payloadEnd := off, off+int(plen)
		off = payloadEnd
		if id != customSectionID {
			continue
		}

		// Custom section: a LEB128 name length, the name, then the body.
		lenBuf := scratch[:min(5, payloadEnd-payloadStart)]
		if err := s.readAt(lenBuf, payloadStart); err != nil {
			return nil, fmt.Errorf("custom section name at offset %d: %w", start, err)
		}
		lr := reader{buf: lenBuf}
		nlen, err := lr.u32()
		if err != nil {
			return nil, fmt.Errorf("custom section name at offset %d: %w", start, err)
		}
		nameStart := payloadStart + lr.off
		if rem := payloadEnd - nameStart; int64(nlen) > int64(rem) {
			return nil, fmt.Errorf("custom section name at offset %d: %w: size %d exceeds %d bytes remaining", start, ErrTruncated, nlen, rem)
		}
		bodyStart := nameStart + int(nlen)

		var name string
		matched := false
		if int(nlen) <= maxName {
			nb := nameBuf[:nlen]
			if err := s.readAt(nb, nameStart); err != nil {
				return nil, fmt.Errorf("custom section name at offset %d: %w", start, err)
			}
			if !utf8.Valid(nb) {
				return nil, fmt.Errorf("custom section name at offset %d: %w: name is not UTF-8", start, ErrMalformed)
			}
			if want[string(nb)] {
				matched, name = true, string(nb)
			}
		} else {
			// Sized to the name, up to nameChunk: real unrequested names,
			// such as contractenvmetav0, are a few bytes long.
			if want := min(int(nlen), nameChunk) + utf8.UTFMax - 1; len(chunk) < want {
				chunk = make([]byte, want)
			}
			ok, err := s.validUTF8(chunk, nameStart, bodyStart)
			if err != nil {
				return nil, fmt.Errorf("custom section name at offset %d: %w", start, err)
			}
			if !ok {
				return nil, fmt.Errorf("custom section name at offset %d: %w: name is not UTF-8", start, ErrMalformed)
			}
		}
		if !matched {
			continue
		}

		blen := payloadEnd - bodyStart
		total := len(out[name]) + blen
		if total > lim.MaxSectionBytes {
			return nil, &LimitError{Limit: "MaxSectionBytes", Max: lim.MaxSectionBytes, Got: total}
		}
		prev := out[name]
		grown := slices.Grow(prev, blen)[:len(prev)+blen]
		if err := s.readAt(grown[len(prev):], bodyStart); err != nil {
			return nil, fmt.Errorf("custom section %q at offset %d: %w", name, start, err)
		}
		out[name] = grown
	}
	return out, nil
}

// streamReader reads exact ranges of a module of known size.
type streamReader struct {
	r    io.ReaderAt
	size int
}

// readAt fills p from offset off. The caller has already checked that the
// range lies inside size, so an early end of input means r is shorter than
// it claimed: ErrTruncated.
func (s streamReader) readAt(p []byte, off int) error {
	if len(p) == 0 {
		return nil
	}
	n, err := s.r.ReadAt(p, int64(off))
	if n == len(p) {
		return nil // io.ReaderAt may return io.EOF with a full read at the end
	}
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%w: read %d of %d bytes at offset %d", ErrTruncated, n, len(p), off)
	}
	return fmt.Errorf("read %d bytes at offset %d: %w", len(p), off, err)
}

// validUTF8 reports whether bytes [from, to) are valid UTF-8, reading them in
// chunks through buf so a long name never needs a buffer of its own size. buf
// must be at least utf8.UTFMax bytes longer than one chunk's worth of reading
// room; the extra bytes carry a character split across two chunks.
func (s streamReader) validUTF8(buf []byte, from, to int) (bool, error) {
	room := len(buf) - (utf8.UTFMax - 1)
	carry := 0 // bytes at buf[0:carry] left over from the previous chunk
	for from < to {
		n := min(room, to-from)
		if err := s.readAt(buf[carry:carry+n], from); err != nil {
			return false, err
		}
		from += n
		data := buf[:carry+n]
		cut := len(data)
		if from < to {
			cut = completeRunes(data)
		}
		if !utf8.Valid(data[:cut]) {
			return false, nil
		}
		carry = copy(buf, data[cut:])
	}
	return carry == 0, nil
}

// completeRunes returns the length of the longest prefix of b that does not
// end inside a multi-byte character that more bytes could still complete.
// Bytes that can never complete are left in the prefix for utf8.Valid to
// reject.
func completeRunes(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-(utf8.UTFMax-1); i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return i
			}
			return len(b)
		}
	}
	return len(b)
}

func clampInt(v int64) int {
	const maxInt = int64(^uint(0) >> 1)
	if v > maxInt {
		return int(maxInt)
	}
	return int(v)
}
