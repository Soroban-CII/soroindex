// Package sepmeta reads the SEP-47 declarations and the interface spec that
// a Soroban contract embeds in its Wasm custom sections.
//
// Every input to this package is treated as hostile: Wasm bytes and XDR come
// from the network and may be crafted. Each length read from them is checked
// against the bytes remaining and against a configured [Limits] before any
// allocation, and malformed input is returned as an error, never a panic.
// The package only parses bytes; it never executes Wasm.
package sepmeta
