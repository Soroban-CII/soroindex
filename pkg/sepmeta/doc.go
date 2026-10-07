// Package sepmeta reads the SEP-47 declarations and the interface spec that
// a Soroban contract embeds in its Wasm custom sections.
//
// A contract states which SEPs it implements in a "sep" entry of its
// contractmetav0 section (SEP-47), and describes its functions in its
// contractspecv0 section. This package turns both into plain Go values:
//
//	secs, err := sepmeta.ReadCustomSections(wasm,
//		[]string{sepmeta.SectionMeta, sepmeta.SectionSpec}, sepmeta.DefaultLimits())
//	entries, err := sepmeta.DecodeMeta(secs[sepmeta.SectionMeta], sepmeta.DefaultLimits())
//	claims := sepmeta.ParseSEP47(entries, sepmeta.Lenient)  // claims.SEPs == [41]
//	spec, err := sepmeta.DecodeSpec(secs[sepmeta.SectionSpec], sepmeta.DefaultLimits())
//	fns := sepmeta.Functions(spec)                          // name + type names
//
// A declaration is what the contract says, not what it does. SEP-47 leaves
// claims unverified, and so does this package: compare Functions against the
// SEP's interface to check the claim, and remember that even a matching
// interface says nothing about behavior.
//
// # Untrusted input
//
// Every input to this package is treated as hostile: Wasm bytes and XDR come
// from the network and may be crafted. Each length read from them is checked
// against the bytes remaining and against a configured [Limits] before any
// allocation, and malformed input is returned as an error, never a panic.
// Errors match one of [ErrNotWasm], [ErrTruncated], [ErrMalformed] or
// [ErrLimit] under errors.Is. The package only parses bytes; it never
// executes Wasm.
//
// # Versioning
//
// [ParserVersion] changes whenever the output for some input changes, so
// stored results can be traced to the rules that produced them.
package sepmeta
