package sepmeta

import (
	"bytes"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// ParserVersion identifies the parsing rules in this package. It is stored
// with every derived row and returned by the API, so a change to what the
// parser accepts can be traced to the data it produced. Bump it whenever the
// output for some input changes.
const ParserVersion = "1"

// Section names defined by SEP-46 (meta) and the Soroban spec format.
const (
	SectionMeta = "contractmetav0"
	SectionSpec = "contractspecv0"
)

// MetaEntry is one key/value pair from a contract's contractmetav0 section.
// Values are kept as raw strings; they are not required to be UTF-8.
type MetaEntry struct {
	Key   string
	Value string
}

// DecodeMeta decodes a contractmetav0 payload: a back-to-back stream of XDR
// SCMetaEntry values with no count prefix. It returns the entries decoded
// before any error, so a partly damaged section still yields what it can.
//
// A trailing entry cut short is ErrTruncated; bytes that cannot begin any
// valid entry are ErrMalformed.
func DecodeMeta(section []byte, lim Limits) ([]MetaEntry, error) {
	raw, err := decodeStream[xdr.ScMetaEntry](section, lim)
	out := make([]MetaEntry, 0, len(raw))
	for _, e := range raw {
		// V0 is the only SCMetaKind; decodeStream rejects any other as malformed.
		out = append(out, MetaEntry{Key: e.V0.Key, Value: e.V0.Val})
	}
	return out, err
}

// decodeStream decodes consecutive XDR values of type T until the input is
// exhausted. Each value is decoded with lengths bounded by the bytes that
// remain and nesting bounded by lim.MaxXDRDepth.
func decodeStream[T any](section []byte, lim Limits) ([]T, error) {
	lim = lim.withDefaults()
	if len(section) > lim.MaxSectionBytes {
		return nil, &LimitError{Limit: "MaxSectionBytes", Max: lim.MaxSectionBytes, Got: len(section)}
	}
	var out []T
	r := bytes.NewReader(section)
	for r.Len() > 0 {
		start := len(section) - r.Len()
		var v T
		if _, err := xdr.UnmarshalWithOptions(r, &v, decodeOptions(r.Len(), lim)); err != nil {
			kind := ErrMalformed
			if completesWithPadding[T](section[start:], lim) {
				kind = ErrTruncated
			}
			return out, fmt.Errorf("entry %d at offset %d: %w: %w", len(out), start, kind, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// completesWithPadding reports whether rest is a strict prefix of a valid
// encoding of T: that is, whether the decode failed only because bytes were
// missing.
//
// XDR values occupy whole 4-byte words and every entry starts on a word
// boundary, so a trailing partial word is dropped first: its missing bytes
// could be anything, and zeros would not complete, say, the type code
// 0x000003EA cut to [0 0 3]. The whole words that remain are then followed
// by lim.MaxSectionBytes zero bytes and decoded again. Zeros complete any
// value that stops at a word boundary (a zero length, a zero discriminant,
// an absent optional), while words that are wrong in themselves still fail.
// Only the error path pays for this.
func completesWithPadding[T any](rest []byte, lim Limits) bool {
	whole := len(rest) - len(rest)%4
	padded := make([]byte, whole+lim.MaxSectionBytes)
	copy(padded, rest[:whole])
	var v T
	n, err := xdr.UnmarshalWithOptions(bytes.NewReader(padded), &v, decodeOptions(len(padded), lim))
	return err == nil && n > whole
}

func decodeOptions(inputLen int, lim Limits) xdr.DecodeOptions {
	return xdr.DecodeOptions{MaxDepth: lim.MaxXDRDepth, MaxInputLen: inputLen}
}
