package sepmeta

import (
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// ErrTypeTable means a spec's user-defined types could not be resolved. The
// wrapped error names the type or types involved; the table is still returned
// so a caller can show what was read.
var ErrTypeTable = errors.New("type table")

// Type kinds as they appear in a spec entry. They are the names rule files
// and the CLI use.
const (
	TypeKindStruct    = "struct"
	TypeKindUnion     = "union"
	TypeKindEnum      = "enum"
	TypeKindErrorEnum = "error_enum"
)

// TypeField is one struct field. Type is rendered by TypeName, so a nested
// user-defined type reads "udt:Price".
type TypeField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// TypeCase is one union case. A void case has no types; a tuple case has one
// or more, in order.
type TypeCase struct {
	Name  string   `json:"name"`
	Types []string `json:"types"`
}

// TypeValue is one enum or error-enum value. The SDK assigns explicit
// integers, so the name alone is not enough for a rule file to compare.
type TypeValue struct {
	Name  string `json:"name"`
	Value uint32 `json:"value"`
}

// TypeDef is one user-defined type with its structure. Exactly one of Fields,
// Cases and Values is set, according to Kind.
type TypeDef struct {
	Name   string      `json:"name"`
	Kind   string      `json:"kind"`
	Fields []TypeField `json:"fields,omitempty"`
	Cases  []TypeCase  `json:"cases,omitempty"`
	Values []TypeValue `json:"values,omitempty"`
}

// Types decodes every user-defined type entry in a spec and checks that every
// udt: reference made by a function or by another type resolves. The table is
// returned even when err is non-nil, in spec order, so a caller can show what
// was read before the first problem.
//
// Undefined references and reference cycles are errors naming the types
// involved. Traversal is bounded by the table size, so a cycle can never be
// followed forever. Zero fields in lim take their DefaultLimits value.
func Types(spec []xdr.ScSpecEntry, lim Limits) ([]TypeDef, error) {
	lim = lim.withDefaults()
	if len(spec) > lim.MaxTypeEntries {
		return nil, &LimitError{Limit: "MaxTypeEntries", Max: lim.MaxTypeEntries, Got: len(spec)}
	}
	var defs []TypeDef
	index := map[string]int{}
	var errs []error
	for _, e := range spec {
		var d TypeDef
		var ok bool
		switch e.Kind {
		case xdr.ScSpecEntryKindScSpecEntryUdtStructV0:
			if e.UdtStructV0 != nil {
				d, ok = structDef(e.UdtStructV0)
			}
		case xdr.ScSpecEntryKindScSpecEntryUdtUnionV0:
			if e.UdtUnionV0 != nil {
				d, ok = unionDef(e.UdtUnionV0)
			}
		case xdr.ScSpecEntryKindScSpecEntryUdtEnumV0:
			if e.UdtEnumV0 != nil {
				d, ok = enumDef(e.UdtEnumV0)
			}
		case xdr.ScSpecEntryKindScSpecEntryUdtErrorEnumV0:
			if e.UdtErrorEnumV0 != nil {
				d, ok = errorEnumDef(e.UdtErrorEnumV0)
			}
		}
		if !ok {
			continue
		}
		if err := checkName(d.Name, lim); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, dup := index[d.Name]; dup {
			errs = append(errs, fmt.Errorf("%w: type %q is defined more than once", ErrTypeTable, d.Name))
			continue
		}
		index[d.Name] = len(defs)
		defs = append(defs, d)
	}

	// Every reference from a type and from a function signature must resolve.
	// A missing name is reported once per referring type, not once per use.
	for _, d := range defs {
		if err := checkDefNames(d, lim); err != nil {
			errs = append(errs, err)
		}
		for _, ref := range defRefs(d) {
			if _, ok := index[ref]; !ok {
				errs = append(errs, fmt.Errorf("%w: type %q references undefined type %q", ErrTypeTable, d.Name, ref))
			}
		}
	}
	for _, e := range spec {
		if e.Kind != xdr.ScSpecEntryKindScSpecEntryFunctionV0 || e.FunctionV0 == nil {
			continue
		}
		var refs []string
		for _, in := range e.FunctionV0.Inputs {
			refs = append(refs, typeRefs(in.Type)...)
		}
		for _, out := range e.FunctionV0.Outputs {
			refs = append(refs, typeRefs(out)...)
		}
		for _, ref := range refs {
			if _, ok := index[ref]; !ok {
				errs = append(errs, fmt.Errorf("%w: function %q references undefined type %q", ErrTypeTable, e.FunctionV0.Name, ref))
			}
		}
	}
	if err := checkCycles(defs); err != nil {
		errs = append(errs, err)
	}
	return defs, errors.Join(errs...)
}

// ResolveType returns the definition a udt: reference names. Accepting both
// "udt:Foo" and "Foo" keeps callers from having to strip the prefix.
func ResolveType(defs []TypeDef, name string) (TypeDef, bool) {
	if len(name) > 4 && name[:4] == "udt:" {
		name = name[4:]
	}
	for _, d := range defs {
		if d.Name == name {
			return d, true
		}
	}
	return TypeDef{}, false
}

func structDef(s *xdr.ScSpecUdtStructV0) (TypeDef, bool) {
	d := TypeDef{Name: s.Name, Kind: TypeKindStruct, Fields: make([]TypeField, 0, len(s.Fields))}
	for _, f := range s.Fields {
		d.Fields = append(d.Fields, TypeField{Name: f.Name, Type: TypeName(f.Type)})
	}
	return d, true
}

func unionDef(u *xdr.ScSpecUdtUnionV0) (TypeDef, bool) {
	d := TypeDef{Name: u.Name, Kind: TypeKindUnion, Cases: make([]TypeCase, 0, len(u.Cases))}
	for _, c := range u.Cases {
		switch c.Kind {
		case xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseVoidV0:
			if c.VoidCase == nil {
				return TypeDef{}, false
			}
			d.Cases = append(d.Cases, TypeCase{Name: c.VoidCase.Name, Types: []string{}})
		case xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseTupleV0:
			if c.TupleCase == nil {
				return TypeDef{}, false
			}
			tc := TypeCase{Name: c.TupleCase.Name, Types: make([]string, 0, len(c.TupleCase.Type))}
			for _, t := range c.TupleCase.Type {
				tc.Types = append(tc.Types, TypeName(t))
			}
			d.Cases = append(d.Cases, tc)
		default:
			return TypeDef{}, false
		}
	}
	return d, true
}

func enumDef(e *xdr.ScSpecUdtEnumV0) (TypeDef, bool) {
	d := TypeDef{Name: e.Name, Kind: TypeKindEnum, Values: make([]TypeValue, 0, len(e.Cases))}
	for _, c := range e.Cases {
		d.Values = append(d.Values, TypeValue{Name: c.Name, Value: uint32(c.Value)})
	}
	return d, true
}

func errorEnumDef(e *xdr.ScSpecUdtErrorEnumV0) (TypeDef, bool) {
	d := TypeDef{Name: e.Name, Kind: TypeKindErrorEnum, Values: make([]TypeValue, 0, len(e.Cases))}
	for _, c := range e.Cases {
		d.Values = append(d.Values, TypeValue{Name: c.Name, Value: uint32(c.Value)})
	}
	return d, true
}

// defRefs returns the distinct udt names a definition mentions, directly or
// inside a container type.
func defRefs(d TypeDef) []string {
	var refs []string
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			refs = append(refs, name)
		}
	}
	for _, f := range d.Fields {
		if name, ok := udtName(f.Type); ok {
			add(name)
		}
	}
	for _, c := range d.Cases {
		for _, t := range c.Types {
			if name, ok := udtName(t); ok {
				add(name)
			}
		}
	}
	return refs
}

// udtName extracts the type name from a rendered type such as "udt:Foo",
// "Option<udt:Foo>" or "Map<Symbol,udt:Foo>". Rendering is flat enough that a
// scan for the prefix is exact: user type names come from the spec, never
// from a caller.
func udtName(rendered string) (string, bool) {
	const p = "udt:"
	for i := 0; i+len(p) <= len(rendered); i++ {
		if rendered[i:i+len(p)] != p {
			continue
		}
		start := i + len(p)
		end := start
		for end < len(rendered) {
			c := rendered[end]
			if c == '>' || c == ',' || c == ')' {
				break
			}
			end++
		}
		if end > start {
			return rendered[start:end], true
		}
	}
	return "", false
}

// typeRefs returns every udt name in one spec type, including inside
// containers.
func typeRefs(t xdr.ScSpecTypeDef) []string {
	switch t.Type {
	case xdr.ScSpecTypeScSpecTypeOption:
		if t.Option != nil {
			return typeRefs(t.Option.ValueType)
		}
	case xdr.ScSpecTypeScSpecTypeResult:
		if t.Result != nil {
			return append(typeRefs(t.Result.OkType), typeRefs(t.Result.ErrorType)...)
		}
	case xdr.ScSpecTypeScSpecTypeVec:
		if t.Vec != nil {
			return typeRefs(t.Vec.ElementType)
		}
	case xdr.ScSpecTypeScSpecTypeMap:
		if t.Map != nil {
			return append(typeRefs(t.Map.KeyType), typeRefs(t.Map.ValueType)...)
		}
	case xdr.ScSpecTypeScSpecTypeTuple:
		if t.Tuple != nil {
			var out []string
			for _, v := range t.Tuple.ValueTypes {
				out = append(out, typeRefs(v)...)
			}
			return out
		}
	case xdr.ScSpecTypeScSpecTypeUdt:
		if t.Udt != nil && t.Udt.Name != "" {
			return []string{t.Udt.Name}
		}
	}
	return nil
}

func checkName(name string, lim Limits) error {
	if name == "" {
		return fmt.Errorf("%w: a type has an empty name", ErrTypeTable)
	}
	if len(name) > lim.MaxTypeNameLen {
		return &LimitError{Limit: "MaxTypeNameLen", Max: lim.MaxTypeNameLen, Got: len(name)}
	}
	return nil
}

func checkDefNames(d TypeDef, lim Limits) error {
	var errs []error
	for _, f := range d.Fields {
		if err := checkName(f.Name, lim); err != nil {
			errs = append(errs, fmt.Errorf("type %q field: %w", d.Name, err))
		}
	}
	for _, c := range d.Cases {
		if err := checkName(c.Name, lim); err != nil {
			errs = append(errs, fmt.Errorf("type %q case: %w", d.Name, err))
		}
	}
	for _, v := range d.Values {
		if err := checkName(v.Name, lim); err != nil {
			errs = append(errs, fmt.Errorf("type %q value: %w", d.Name, err))
		}
	}
	return errors.Join(errs...)
}

// checkCycles walks the reference graph. Each definition is visited once, so
// the walk is bounded even when a cycle exists; a back edge reports the whole
// cycle by name.
func checkCycles(defs []TypeDef) error {
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(defs))
	var path []string
	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case done:
			return nil
		case visiting:
			start := 0
			for i, p := range path {
				if p == name {
					start = i
					break
				}
			}
			cycle := append(append([]string{}, path[start:]...), name)
			return fmt.Errorf("%w: reference cycle %v", ErrTypeTable, cycle)
		}
		state[name] = visiting
		path = append(path, name)
		for _, d := range defs {
			if d.Name != name {
				continue
			}
			for _, ref := range defRefs(d) {
				if err := visit(ref); err != nil {
					return err
				}
			}
			break
		}
		path = path[:len(path)-1]
		state[name] = done
		return nil
	}
	for _, d := range defs {
		if err := visit(d.Name); err != nil {
			return err
		}
	}
	return nil
}
