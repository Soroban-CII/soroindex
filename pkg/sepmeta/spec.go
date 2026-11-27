package sepmeta

import (
	"strconv"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// DecodeSpec decodes a contractspecv0 payload: a back-to-back stream of XDR
// SCSpecEntry values. It follows the same rules as DecodeMeta: entries
// decoded before an error are returned with it, a trailing entry cut short is
// ErrTruncated, bytes that cannot begin any valid entry are ErrMalformed, and
// a type nested deeper than lim.MaxXDRDepth is ErrLimit.
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

type UDTKind string

const (
	UDTKindStruct    UDTKind = "struct"
	UDTKindUnion     UDTKind = "union"
	UDTKindEnum      UDTKind = "enum"
	UDTKindErrorEnum UDTKind = "error_enum"
)

type StructField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type UnionCase struct {
	Name  string   `json:"name"`
	Types []string `json:"types"`
}

type EnumCase struct {
	Name  string `json:"name"`
	Value uint32 `json:"value"`
}

type UDTDef struct {
	Name   string        `json:"name"`
	Kind   UDTKind       `json:"kind"`
	Fields []StructField `json:"fields,omitempty"`
	Cases  []UnionCase   `json:"cases,omitempty"`
	Enums  []EnumCase    `json:"enums,omitempty"`
}

type TypeTable map[string]UDTDef

func Types(spec []xdr.ScSpecEntry) TypeTable {
	table := make(TypeTable)
	for _, e := range spec {
		switch e.Kind {
		case xdr.ScSpecEntryKindScSpecEntryUdtStructV0:
			if e.UdtStructV0 == nil {
				continue
			}
			s := e.UdtStructV0
			def := UDTDef{Name: s.Name, Kind: UDTKindStruct, Fields: make([]StructField, 0, len(s.Fields))}
			for _, f := range s.Fields {
				def.Fields = append(def.Fields, StructField{Name: f.Name, Type: TypeName(f.Type)})
			}
			table[s.Name] = def
		case xdr.ScSpecEntryKindScSpecEntryUdtUnionV0:
			if e.UdtUnionV0 == nil {
				continue
			}
			u := e.UdtUnionV0
			def := UDTDef{Name: u.Name, Kind: UDTKindUnion, Cases: make([]UnionCase, 0, len(u.Cases))}
			for _, c := range u.Cases {
				if tc, ok := c.GetTupleCase(); ok {
					types := make([]string, 0, len(tc.Type))
					for _, ty := range tc.Type {
						types = append(types, TypeName(ty))
					}
					def.Cases = append(def.Cases, UnionCase{Name: string(tc.Name), Types: types})
				} else if vc, ok := c.GetVoidCase(); ok {
					def.Cases = append(def.Cases, UnionCase{Name: string(vc.Name), Types: []string{}})
				}
			}
			table[u.Name] = def
		case xdr.ScSpecEntryKindScSpecEntryUdtEnumV0:
			if e.UdtEnumV0 == nil {
				continue
			}
			en := e.UdtEnumV0
			def := UDTDef{Name: en.Name, Kind: UDTKindEnum, Enums: make([]EnumCase, 0, len(en.Cases))}
			for _, c := range en.Cases {
				def.Enums = append(def.Enums, EnumCase{Name: c.Name, Value: uint32(c.Value)})
			}
			table[en.Name] = def
		case xdr.ScSpecEntryKindScSpecEntryUdtErrorEnumV0:
			if e.UdtErrorEnumV0 == nil {
				continue
			}
			ee := e.UdtErrorEnumV0
			def := UDTDef{Name: ee.Name, Kind: UDTKindErrorEnum, Enums: make([]EnumCase, 0, len(ee.Cases))}
			for _, c := range ee.Cases {
				def.Enums = append(def.Enums, EnumCase{Name: c.Name, Value: uint32(c.Value)})
			}
			table[ee.Name] = def
		}
	}
	return table
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
