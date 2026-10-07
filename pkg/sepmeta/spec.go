package sepmeta

import (
	"strconv"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// DecodeSpec decodes a contractspecv0 payload: a back-to-back stream of XDR
// SCSpecEntry values. It follows the same rules as DecodeMeta: entries
// decoded before an error are returned with it, a trailing entry cut short is
// ErrTruncated, and bytes that cannot begin any valid entry are ErrMalformed.
func DecodeSpec(section []byte, lim Limits) ([]xdr.ScSpecEntry, error) {
	return decodeStream[xdr.ScSpecEntry](section, lim)
}

// FnSig is a function's interface as the type checker sees it: its name and
// the types of its inputs and outputs, in order. Parameter names are left
// out on purpose; a SEP's interface is defined by types, and the matcher must
// not depend on what an author called a parameter.
type FnSig struct {
	Name    string   `json:"name"`
	Inputs  []string `json:"inputs"`
	Outputs []string `json:"outputs"`
}

// Functions returns the function entries of a spec in spec order, with each
// type rendered by TypeName. Struct, union, enum, error and event entries are
// skipped. Duplicate names are returned as they appear; deciding what a
// duplicate means is the matcher's job.
func Functions(spec []xdr.ScSpecEntry) []FnSig {
	var out []FnSig
	for _, e := range spec {
		if e.Kind != xdr.ScSpecEntryKindScSpecEntryFunctionV0 || e.FunctionV0 == nil {
			continue
		}
		f := e.FunctionV0
		sig := FnSig{Name: string(f.Name), Inputs: []string{}, Outputs: []string{}}
		for _, in := range f.Inputs {
			sig.Inputs = append(sig.Inputs, TypeName(in.Type))
		}
		for _, o := range f.Outputs {
			sig.Outputs = append(sig.Outputs, TypeName(o))
		}
		out = append(out, sig)
	}
	return out
}

// scalarNames maps every non-parameterized spec type to its canonical name.
// The names follow the Rust SDK's spelling, which is what SEP documents use.
var scalarNames = map[xdr.ScSpecType]string{
	xdr.ScSpecTypeScSpecTypeVal:          "Val",
	xdr.ScSpecTypeScSpecTypeBool:         "bool",
	xdr.ScSpecTypeScSpecTypeVoid:         "void",
	xdr.ScSpecTypeScSpecTypeError:        "Error",
	xdr.ScSpecTypeScSpecTypeU32:          "u32",
	xdr.ScSpecTypeScSpecTypeI32:          "i32",
	xdr.ScSpecTypeScSpecTypeU64:          "u64",
	xdr.ScSpecTypeScSpecTypeI64:          "i64",
	xdr.ScSpecTypeScSpecTypeTimepoint:    "Timepoint",
	xdr.ScSpecTypeScSpecTypeDuration:     "Duration",
	xdr.ScSpecTypeScSpecTypeU128:         "u128",
	xdr.ScSpecTypeScSpecTypeI128:         "i128",
	xdr.ScSpecTypeScSpecTypeU256:         "u256",
	xdr.ScSpecTypeScSpecTypeI256:         "i256",
	xdr.ScSpecTypeScSpecTypeBytes:        "Bytes",
	xdr.ScSpecTypeScSpecTypeString:       "String",
	xdr.ScSpecTypeScSpecTypeSymbol:       "Symbol",
	xdr.ScSpecTypeScSpecTypeAddress:      "Address",
	xdr.ScSpecTypeScSpecTypeMuxedAddress: "MuxedAddress",
}

// TypeName renders a spec type canonically: scalars by name ("i128",
// "MuxedAddress"), containers as Option<T>, Vec<T>, Map<K,V>, Result<T,E>,
// (A,B) for tuples and BytesN<32>, and user-defined types as "udt:<Name>".
// The rendering is the comparison key for rule files, so it must be stable.
//
// A union arm that the decoder left nil (which a well-formed decode never
// does) renders as "invalid" rather than panicking.
func TypeName(t xdr.ScSpecTypeDef) string {
	if n, ok := scalarNames[t.Type]; ok {
		return n
	}
	switch t.Type {
	case xdr.ScSpecTypeScSpecTypeOption:
		if t.Option != nil {
			return "Option<" + TypeName(t.Option.ValueType) + ">"
		}
	case xdr.ScSpecTypeScSpecTypeResult:
		if t.Result != nil {
			return "Result<" + TypeName(t.Result.OkType) + "," + TypeName(t.Result.ErrorType) + ">"
		}
	case xdr.ScSpecTypeScSpecTypeVec:
		if t.Vec != nil {
			return "Vec<" + TypeName(t.Vec.ElementType) + ">"
		}
	case xdr.ScSpecTypeScSpecTypeMap:
		if t.Map != nil {
			return "Map<" + TypeName(t.Map.KeyType) + "," + TypeName(t.Map.ValueType) + ">"
		}
	case xdr.ScSpecTypeScSpecTypeTuple:
		if t.Tuple != nil {
			parts := make([]string, len(t.Tuple.ValueTypes))
			for i, v := range t.Tuple.ValueTypes {
				parts[i] = TypeName(v)
			}
			return "(" + strings.Join(parts, ",") + ")"
		}
	case xdr.ScSpecTypeScSpecTypeBytesN:
		if t.BytesN != nil {
			return "BytesN<" + strconv.FormatUint(uint64(t.BytesN.N), 10) + ">"
		}
	case xdr.ScSpecTypeScSpecTypeUdt:
		if t.Udt != nil {
			return "udt:" + t.Udt.Name
		}
	default:
		return "unknown:" + strconv.Itoa(int(t.Type))
	}
	return "invalid"
}
