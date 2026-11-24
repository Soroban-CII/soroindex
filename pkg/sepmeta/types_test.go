package sepmeta

import (
	"errors"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// udt builds a spec type that refers to a user-defined type by name.
func udt(name string) xdr.ScSpecTypeDef {
	return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeUdt, Udt: &xdr.ScSpecTypeUdt{Name: name}}
}

func optional(inner xdr.ScSpecTypeDef) xdr.ScSpecTypeDef {
	return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeOption, Option: &xdr.ScSpecTypeOption{ValueType: inner}}
}

func structEntry(name string, fields ...xdr.ScSpecUdtStructFieldV0) xdr.ScSpecEntry {
	return xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryUdtStructV0,
		UdtStructV0: &xdr.ScSpecUdtStructV0{Name: name, Fields: fields}}
}

func unionEntry(name string, cases ...xdr.ScSpecUdtUnionCaseV0) xdr.ScSpecEntry {
	return xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryUdtUnionV0,
		UdtUnionV0: &xdr.ScSpecUdtUnionV0{Name: name, Cases: cases}}
}

func enumEntry(name string, cases ...xdr.ScSpecUdtEnumCaseV0) xdr.ScSpecEntry {
	return xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryUdtEnumV0,
		UdtEnumV0: &xdr.ScSpecUdtEnumV0{Name: name, Cases: cases}}
}

func errorEnumEntry(name string, cases ...xdr.ScSpecUdtErrorEnumCaseV0) xdr.ScSpecEntry {
	return xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryUdtErrorEnumV0,
		UdtErrorEnumV0: &xdr.ScSpecUdtErrorEnumV0{Name: name, Cases: cases}}
}

func voidCase(name string) xdr.ScSpecUdtUnionCaseV0 {
	return xdr.ScSpecUdtUnionCaseV0{
		Kind:     xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseVoidV0,
		VoidCase: &xdr.ScSpecUdtUnionCaseVoidV0{Name: name},
	}
}

func tupleCase(name string, types ...xdr.ScSpecTypeDef) xdr.ScSpecUdtUnionCaseV0 {
	return xdr.ScSpecUdtUnionCaseV0{
		Kind:      xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseTupleV0,
		TupleCase: &xdr.ScSpecUdtUnionCaseTupleV0{Name: name, Type: types},
	}
}

func fnEntry(name string, inputs []xdr.ScSpecFunctionInputV0, outputs []xdr.ScSpecTypeDef) xdr.ScSpecEntry {
	return xdr.ScSpecEntry{Kind: xdr.ScSpecEntryKindScSpecEntryFunctionV0,
		FunctionV0: &xdr.ScSpecFunctionV0{Name: xdr.ScSymbol(name), Inputs: inputs, Outputs: outputs}}
}

// specStream encodes entries and decodes them again, so every test starts
// from a real contractspecv0 stream rather than the structs it was built
// from. That is what the parser will see on chain.
func specStream(t *testing.T, entries ...xdr.ScSpecEntry) []xdr.ScSpecEntry {
	t.Helper()
	var stream []byte
	for i, e := range entries {
		b, err := e.MarshalBinary()
		if err != nil {
			t.Fatalf("entry %d: encode: %v", i, err)
		}
		stream = append(stream, b...)
	}
	got, err := DecodeSpec(stream, Limits{})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(entries) {
		t.Fatalf("decoded %d entries, want %d", len(got), len(entries))
	}
	return got
}

func TestTypesDecodesEveryKind(t *testing.T) {
	spec := specStream(t,
		structEntry("Asset",
			xdr.ScSpecUdtStructFieldV0{Name: "code", Type: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeSymbol}},
			xdr.ScSpecUdtStructFieldV0{Name: "issuer", Type: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeAddress}},
			xdr.ScSpecUdtStructFieldV0{Name: "price", Type: udt("Price")},
		),
		structEntry("Price",
			xdr.ScSpecUdtStructFieldV0{Name: "n", Type: xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeI128}},
		),
		unionEntry("Change",
			voidCase("none"),
			tupleCase("amount", xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU32}, xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeI128}),
			tupleCase("asset", udt("Asset")),
		),
		enumEntry("Color",
			xdr.ScSpecUdtEnumCaseV0{Name: "red", Value: 0},
			xdr.ScSpecUdtEnumCaseV0{Name: "blue", Value: 7},
		),
		errorEnumEntry("Failure",
			xdr.ScSpecUdtErrorEnumCaseV0{Name: "not_found", Value: 1},
			xdr.ScSpecUdtErrorEnumCaseV0{Name: "denied", Value: 99},
		),
	)

	defs, err := Types(spec, Limits{})
	if err != nil {
		t.Fatalf("Types: %v", err)
	}
	if len(defs) != 5 {
		t.Fatalf("got %d definitions, want 5", len(defs))
	}

	want := []TypeDef{
		{Name: "Asset", Kind: TypeKindStruct, Fields: []TypeField{
			{Name: "code", Type: "Symbol"}, {Name: "issuer", Type: "Address"}, {Name: "price", Type: "udt:Price"}}},
		{Name: "Price", Kind: TypeKindStruct, Fields: []TypeField{{Name: "n", Type: "i128"}}},
		{Name: "Change", Kind: TypeKindUnion, Cases: []TypeCase{
			{Name: "none", Types: []string{}},
			{Name: "amount", Types: []string{"u32", "i128"}},
			{Name: "asset", Types: []string{"udt:Asset"}},
		}},
		{Name: "Color", Kind: TypeKindEnum, Values: []TypeValue{{Name: "red", Value: 0}, {Name: "blue", Value: 7}}},
		{Name: "Failure", Kind: TypeKindErrorEnum, Values: []TypeValue{{Name: "not_found", Value: 1}, {Name: "denied", Value: 99}}},
	}
	for i := range want {
		if !typeDefEqual(defs[i], want[i]) {
			t.Errorf("definition %d = %+v, want %+v", i, defs[i], want[i])
		}
	}

	if d, ok := ResolveType(defs, "udt:Price"); !ok || d.Name != "Price" {
		t.Fatalf("ResolveType(udt:Price) = %+v, %v", d, ok)
	}
	if d, ok := ResolveType(defs, "Asset"); !ok || d.Kind != TypeKindStruct {
		t.Fatalf("ResolveType(Asset) = %+v, %v", d, ok)
	}
	if _, ok := ResolveType(defs, "Missing"); ok {
		t.Fatal("ResolveType found an undefined type")
	}
}

func typeDefEqual(a, b TypeDef) bool {
	if a.Name != b.Name || a.Kind != b.Kind || len(a.Fields) != len(b.Fields) || len(a.Cases) != len(b.Cases) || len(a.Values) != len(b.Values) {
		return false
	}
	for i := range a.Fields {
		if a.Fields[i] != b.Fields[i] {
			return false
		}
	}
	for i := range a.Cases {
		if a.Cases[i].Name != b.Cases[i].Name || len(a.Cases[i].Types) != len(b.Cases[i].Types) {
			return false
		}
		for j := range a.Cases[i].Types {
			if a.Cases[i].Types[j] != b.Cases[i].Types[j] {
				return false
			}
		}
	}
	for i := range a.Values {
		if a.Values[i] != b.Values[i] {
			return false
		}
	}
	return true
}

func TestTypesRejectsUndefinedReference(t *testing.T) {
	spec := specStream(t,
		structEntry("Holder", xdr.ScSpecUdtStructFieldV0{Name: "inner", Type: optional(udt("Missing"))}),
		fnEntry("get", []xdr.ScSpecFunctionInputV0{{Name: "h", Type: udt("Absent")}}, nil),
	)
	defs, err := Types(spec, Limits{})
	if !errors.Is(err, ErrTypeTable) {
		t.Fatalf("err = %v, want ErrTypeTable", err)
	}
	for _, name := range []string{"Holder", "Missing", "get", "Absent"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not name %q: %v", name, err)
		}
	}
	if len(defs) != 1 || defs[0].Name != "Holder" {
		t.Fatalf("defs = %+v, want Holder still returned", defs)
	}
}

func TestTypesRejectsCycle(t *testing.T) {
	spec := specStream(t,
		structEntry("A", xdr.ScSpecUdtStructFieldV0{Name: "b", Type: udt("B")}),
		structEntry("B", xdr.ScSpecUdtStructFieldV0{Name: "a", Type: udt("A")}),
	)
	_, err := Types(spec, Limits{})
	if !errors.Is(err, ErrTypeTable) {
		t.Fatalf("err = %v, want ErrTypeTable", err)
	}
	if !strings.Contains(err.Error(), "cycle") || !strings.Contains(err.Error(), "A") || !strings.Contains(err.Error(), "B") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestTypesBoundsAndDuplicates(t *testing.T) {
	t.Run("entry count", func(t *testing.T) {
		spec := specStream(t, structEntry("A"), structEntry("B"), structEntry("C"))
		_, err := Types(spec, Limits{MaxTypeEntries: 2})
		if !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "MaxTypeEntries") {
			t.Fatalf("err = %v, want MaxTypeEntries limit", err)
		}
	})
	t.Run("name length", func(t *testing.T) {
		spec := specStream(t, structEntry("AVeryLongTypeName"))
		_, err := Types(spec, Limits{MaxTypeNameLen: 4})
		if !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "MaxTypeNameLen") {
			t.Fatalf("err = %v, want MaxTypeNameLen limit", err)
		}
	})
	t.Run("duplicate name", func(t *testing.T) {
		spec := specStream(t, structEntry("A"), enumEntry("A"))
		_, err := Types(spec, Limits{})
		if !errors.Is(err, ErrTypeTable) || !strings.Contains(err.Error(), `"A"`) {
			t.Fatalf("err = %v, want duplicate type error", err)
		}
	})
}
