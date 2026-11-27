package ingest

// Per-purpose base64 ceilings. Each is the length of one base64 XDR string
// the indexer decodes, and each is the value that was hard-coded at its call
// site before the limits became configurable, so a default run decodes
// exactly what it decoded before.
const (
	// DefaultXDRBase64Len is the largest base64 XDR string the indexer
	// accepts anywhere. Ledger metadata is that largest string: testnet
	// ledgers measured on 7 October 2026 were about 200 KB of base64, and
	// 64 MiB leaves ample room for a busy mainnet ledger.
	DefaultXDRBase64Len = 64 << 20
	// DefaultXDRDepth is the XDR nesting depth used when a budget names
	// none. It matches sepmeta.DefaultLimits().MaxXDRDepth.
	DefaultXDRDepth = 500
	// codeXDRBase64Len bounds a CONTRACT_CODE entry when the configured
	// MaxWasmBytes needs less. Code() raises it with MaxWasmBytes so that
	// raising the Wasm limit is enough on its own to index a larger module.
	codeXDRBase64Len = 2 << 20
	// instanceXDRBase64Len bounds a contract-instance entry: instance
	// storage can be large, so the cap is generous but finite.
	instanceXDRBase64Len = 16 << 20
	// execRefXDRBase64Len bounds a CAP-85 executable-reference entry, whose
	// value is one 32-byte hash.
	execRefXDRBase64Len = 4 << 20
	// checkEntryXDRBase64Len bounds an entry re-read by the data-quality
	// check, which compares stored facts with live entries.
	checkEntryXDRBase64Len = 1 << 20
	// codeEntryOverheadBytes covers the CONTRACT_CODE entry around the
	// module: entry type, the 32-byte hash, length prefixes, extension and
	// XDR padding. 1 KiB is far more than the ~64 bytes actually used.
	codeEntryOverheadBytes = 1024
)

// XDRBudget turns the operator's configured ceilings into the XDRLimits each
// decode site uses. It exists so that one validated configuration reaches
// every path that decodes untrusted XDR, instead of each call site carrying
// its own constant.
//
// A zero XDRBudget is the shipped default, so a caller that sets nothing
// decodes exactly what it decoded before the limits were configurable.
type XDRBudget struct {
	// MaxBase64Len is the longest base64 XDR string accepted anywhere, and
	// the limit used for ledger metadata, which is the longest such string.
	// Smaller per-purpose ceilings are clamped by it but not raised to it.
	// Zero means DefaultXDRBase64Len.
	MaxBase64Len int
	// MaxDepth is the XDR nesting depth passed to the SDK decoder. Zero
	// means DefaultXDRDepth.
	MaxDepth uint
	// MaxWasmBytes is the configured sepmeta MaxWasmBytes. It sizes the
	// CONTRACT_CODE ceiling, so a raised Wasm limit is not defeated by a
	// smaller base64 ceiling on the entry that carries the module. Zero
	// means the shipped Wasm default.
	MaxWasmBytes int
}

func (b XDRBudget) withDefaults() XDRBudget {
	if b.MaxBase64Len <= 0 {
		b.MaxBase64Len = DefaultXDRBase64Len
	}
	if b.MaxDepth == 0 {
		b.MaxDepth = DefaultXDRDepth
	}
	return b
}

// For returns the limits for a decode whose own purpose needs at most
// siteMax base64 characters, clamped by the configured ceiling so that
// lowering the ceiling tightens every site.
func (b XDRBudget) For(siteMax int) XDRLimits {
	b = b.withDefaults()
	return XDRLimits{MaxBase64Len: min(siteMax, b.MaxBase64Len), MaxDepth: b.MaxDepth}
}

// Code bounds a CONTRACT_CODE entry. The ceiling grows with MaxWasmBytes,
// because a module that the parser is configured to accept must first get
// through the entry that carries it.
func (b XDRBudget) Code() XDRLimits {
	b = b.withDefaults()
	return b.For(max(codeXDRBase64Len, base64Len(b.MaxWasmBytes+codeEntryOverheadBytes)))
}

// Instance bounds a contract-instance entry.
func (b XDRBudget) Instance() XDRLimits { return b.For(instanceXDRBase64Len) }

// ExecRef bounds a CAP-85 executable-reference entry.
func (b XDRBudget) ExecRef() XDRLimits { return b.For(execRefXDRBase64Len) }

// CheckEntry bounds an entry re-read by the data-quality check.
func (b XDRBudget) CheckEntry() XDRLimits { return b.For(checkEntryXDRBase64Len) }

// LedgerMeta bounds one ledger's metadataXdr, the largest XDR the indexer
// decodes, so it uses the configured ceiling itself: raising the ceiling
// raises this limit, and lowering it lowers this one first.
func (b XDRBudget) LedgerMeta() XDRLimits {
	b = b.withDefaults()
	return XDRLimits{MaxBase64Len: b.MaxBase64Len, MaxDepth: b.MaxDepth}
}

// base64Len is the length of standard base64 for n bytes, padding included.
// n is a configured limit, already bounded well below math.MaxInt/4, so the
// multiplication cannot overflow.
func base64Len(n int) int {
	if n <= 0 {
		return 0
	}
	return 4 * ((n + 2) / 3)
}
