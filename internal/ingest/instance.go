// Package ingest turns ledger data from the network into the facts the
// index stores: which contracts exist, what code each runs, and when that
// changes. All XDR here comes from the network and is decoded with limits.
package ingest

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Sentinel errors.
var (
	// ErrNotInstance means a contract data entry is not a persistent
	// contract-instance entry.
	ErrNotInstance = errors.New("not a contract instance entry")
	// ErrNotExecRef means a contract data entry is not a persistent CAP-85
	// executable reference entry.
	ErrNotExecRef = errors.New("not an executable reference entry")
	// ErrMalformed means an entry decoded as XDR but its contents break an
	// invariant the protocol guarantees, such as a 32-byte Wasm hash.
	ErrMalformed = errors.New("malformed entry")
	// ErrTooLarge means encoded XDR exceeded the configured size cap.
	ErrTooLarge = errors.New("xdr too large")
)

// ExecKind is how a contract's code is found. The values match the
// contracts.kind column (CLAUDE.md §5.8).
type ExecKind string

// Executable kinds.
const (
	KindWasm    ExecKind = "wasm"     // instance holds a Wasm hash
	KindSAC     ExecKind = "sac"      // Stellar Asset Contract, built into the protocol
	KindWasmRef ExecKind = "wasm_ref" // CAP-85: Wasm hash held by another contract's entry
)

// Instance is what the index needs from a contract-instance entry.
type Instance struct {
	ContractID string   // C... strkey
	Kind       ExecKind //
	WasmHash   string   // lowercase hex; set only for KindWasm
	RefOwner   string   // C... strkey; set only for KindWasmRef
	RefTag     string   // set only for KindWasmRef
	// SACAsset is "native" or "CODE:ISSUER" for a SAC whose instance storage
	// carries the asset name, else "". It is read from METADATA.name, which
	// the protocol writes when the SAC is deployed and nothing can change.
	SACAsset string
}

// XDRLimits bounds decoding of untrusted XDR.
type XDRLimits struct {
	MaxBase64Len int  // longest accepted base64 string; default 4 MiB
	MaxDepth     uint // default 500
}

func (l XDRLimits) withDefaults() XDRLimits {
	if l.MaxBase64Len <= 0 {
		l.MaxBase64Len = 4 << 20
	}
	if l.MaxDepth == 0 {
		l.MaxDepth = 500
	}
	return l
}

// UnmarshalBase64 decodes untrusted base64 XDR into dest. The string length
// is checked before any decoding, every XDR length is bounded by the input
// size, nesting is bounded by MaxDepth, and trailing bytes are rejected.
func UnmarshalBase64(s string, dest any, lim XDRLimits) error {
	lim = lim.withDefaults()
	if len(s) > lim.MaxBase64Len {
		return fmt.Errorf("%w: %d base64 chars, limit %d", ErrTooLarge, len(s), lim.MaxBase64Len)
	}
	return xdr.SafeUnmarshalBase64WithOptions(s, dest, xdr.DecodeOptions{MaxDepth: lim.MaxDepth})
}

// ContractIDString renders a contract address as a C... strkey.
func ContractIDString(a xdr.ScAddress) (string, error) {
	if a.Type != xdr.ScAddressTypeScAddressTypeContract || a.ContractId == nil {
		return "", fmt.Errorf("%w: address is not a contract", ErrMalformed)
	}
	return strkey.Encode(strkey.VersionByteContract, a.ContractId[:])
}

// IsInstanceKey reports whether a contract data entry is a contract
// instance: key ScvLedgerKeyContractInstance with persistent durability.
func IsInstanceKey(cd xdr.ContractDataEntry) bool {
	return cd.Key.Type == xdr.ScValTypeScvLedgerKeyContractInstance &&
		cd.Durability == xdr.ContractDataDurabilityPersistent
}

// IsExecRefKey reports whether a contract data entry is a CAP-85
// executable reference: key ScvExecutableTag with persistent durability.
func IsExecRefKey(cd xdr.ContractDataEntry) bool {
	return cd.Key.Type == xdr.ScValTypeScvExecutableTag &&
		cd.Durability == xdr.ContractDataDurabilityPersistent
}

// DecodeInstance extracts the executable from a contract-instance entry.
func DecodeInstance(cd xdr.ContractDataEntry) (Instance, error) {
	if !IsInstanceKey(cd) {
		return Instance{}, ErrNotInstance
	}
	id, err := ContractIDString(cd.Contract)
	if err != nil {
		return Instance{}, err
	}
	inst, ok := cd.Val.GetInstance()
	if !ok {
		return Instance{}, fmt.Errorf("%w: %s: instance value is %s", ErrMalformed, id, cd.Val.Type)
	}
	out := Instance{ContractID: id}
	ex := inst.Executable
	switch ex.Type {
	case xdr.ContractExecutableTypeContractExecutableWasm:
		if ex.WasmHash == nil {
			return Instance{}, fmt.Errorf("%w: %s: wasm executable without hash", ErrMalformed, id)
		}
		out.Kind, out.WasmHash = KindWasm, hex.EncodeToString(ex.WasmHash[:])
	case xdr.ContractExecutableTypeContractExecutableStellarAsset:
		out.Kind = KindSAC
		out.SACAsset = sacAssetName(inst.Storage)
	case xdr.ContractExecutableTypeContractExecutableExternalRef:
		if ex.ExternalRef == nil {
			return Instance{}, fmt.Errorf("%w: %s: external ref executable without reference", ErrMalformed, id)
		}
		owner, err := ContractIDString(ex.ExternalRef.ExecutableOwner)
		if err != nil {
			return Instance{}, fmt.Errorf("%s: reference owner: %w", id, err)
		}
		out.Kind, out.RefOwner, out.RefTag = KindWasmRef, owner, string(ex.ExternalRef.Tag)
	default:
		return Instance{}, fmt.Errorf("%w: %s: unknown executable type %d", ErrMalformed, id, ex.Type)
	}
	return out, nil
}

// sacAssetName reads METADATA.name from a SAC's instance storage. It returns
// "" if the shape differs from what the protocol writes, rather than
// guessing (CLAUDE.md §5.9: leave it NULL if not derivable).
func sacAssetName(storage *xdr.ScMap) string {
	if storage == nil {
		return ""
	}
	for _, e := range *storage {
		if sym, ok := e.Key.GetSym(); !ok || string(sym) != "METADATA" {
			continue
		}
		m, ok := e.Val.GetMap()
		if !ok || m == nil {
			return ""
		}
		for _, f := range *m {
			if sym, ok := f.Key.GetSym(); ok && string(sym) == "name" {
				if s, ok := f.Val.GetStr(); ok && s != "" {
					return string(s)
				}
			}
		}
	}
	return ""
}

// ExecRef is a CAP-85 executable reference entry: the Wasm hash that every
// contract referencing (Owner, Tag) runs.
type ExecRef struct {
	Owner    string // C... strkey
	Tag      string
	WasmHash string // lowercase hex
}

// DecodeExecRef reads an executable reference entry. CAP-85 requires its
// value to be the 32-byte hash of an existing Wasm.
func DecodeExecRef(cd xdr.ContractDataEntry) (ExecRef, error) {
	if !IsExecRefKey(cd) {
		return ExecRef{}, ErrNotExecRef
	}
	owner, err := ContractIDString(cd.Contract)
	if err != nil {
		return ExecRef{}, err
	}
	tag, _ := cd.Key.GetExecutableTag() // IsExecRefKey checked the arm
	b, ok := cd.Val.GetBytes()
	if !ok || len(b) != 32 {
		return ExecRef{}, fmt.Errorf("%w: %s tag %q: value is not a 32-byte hash", ErrMalformed, owner, tag)
	}
	return ExecRef{Owner: owner, Tag: string(tag), WasmHash: hex.EncodeToString(b)}, nil
}

// DecodeContractDataBase64 decodes a base64 ContractDataEntry, as Hubble
// exports it in contract_data_xdr.
func DecodeContractDataBase64(s string, lim XDRLimits) (xdr.ContractDataEntry, error) {
	var cd xdr.ContractDataEntry
	if err := UnmarshalBase64(s, &cd, lim); err != nil {
		return xdr.ContractDataEntry{}, fmt.Errorf("decode contract data: %w", err)
	}
	return cd, nil
}

// DecodeLedgerEntryDataBase64 decodes a base64 LedgerEntryData, as
// getLedgerEntries returns it in "xdr".
func DecodeLedgerEntryDataBase64(s string, lim XDRLimits) (xdr.LedgerEntryData, error) {
	var d xdr.LedgerEntryData
	if err := UnmarshalBase64(s, &d, lim); err != nil {
		return xdr.LedgerEntryData{}, fmt.Errorf("decode ledger entry data: %w", err)
	}
	return d, nil
}

// ContractCodeKey returns the base64 LedgerKey for a Wasm hash (hex).
func ContractCodeKey(hashHex string) (string, error) {
	h, err := ParseWasmHash(hashHex)
	if err != nil {
		return "", err
	}
	return xdr.MarshalBase64(xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.LedgerKeyContractCode{Hash: h}})
}

// InstanceKey returns the base64 LedgerKey of a contract's instance entry.
func InstanceKey(contractID string) (string, error) {
	addr, err := contractAddress(contractID)
	if err != nil {
		return "", err
	}
	return xdr.MarshalBase64(xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractData, ContractData: &xdr.LedgerKeyContractData{
		Contract: addr, Key: xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance}, Durability: xdr.ContractDataDurabilityPersistent,
	}})
}

// ExecRefKey returns the base64 LedgerKey of a CAP-85 reference entry.
func ExecRefKey(owner, tag string) (string, error) {
	addr, err := contractAddress(owner)
	if err != nil {
		return "", err
	}
	t := xdr.ScString(tag)
	return xdr.MarshalBase64(xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractData, ContractData: &xdr.LedgerKeyContractData{
		Contract: addr, Key: xdr.ScVal{Type: xdr.ScValTypeScvExecutableTag, ExecutableTag: &t}, Durability: xdr.ContractDataDurabilityPersistent,
	}})
}

func contractAddress(id string) (xdr.ScAddress, error) {
	raw, err := strkey.Decode(strkey.VersionByteContract, id)
	if err != nil {
		return xdr.ScAddress{}, fmt.Errorf("contract id %q: %w", id, err)
	}
	var cid xdr.ContractId
	copy(cid[:], raw)
	return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &cid}, nil
}

// ParseWasmHash parses 64 lowercase hex characters.
func ParseWasmHash(s string) (xdr.Hash, error) {
	var h xdr.Hash
	if len(s) != 64 {
		return h, fmt.Errorf("wasm hash %q: want 64 hex chars", s)
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return h, fmt.Errorf("wasm hash %q: want lowercase hex", s)
		}
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return h, err
	}
	copy(h[:], b)
	return h, nil
}
