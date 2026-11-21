package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// canonicalType maps common Rust types used in SEP traits to the canonical
// type names used by the matcher (see pkg/sepmeta/spec.go).
var canonicalType = map[string]string{
	"Address":    "Address",
	"MuxedAddress": "MuxedAddress",
	"i128":       "i128",
	"u32":        "u32",
	"String":     "String",
	"u64":        "u64",
	"i64":        "i64",
	"u128":       "u128",
	"i32":        "i32",
	"bool":       "bool",
	"Option<Address>": "Option<Address>",
	"Vec<Address>":  "Vec<Address>",
}

// fnPattern matches a Rust fn declaration within a trait, e.g.
//	fn allowance(env: Env, from: Address, spender: Address) -> i128;
var fnPattern = regexp.MustCompile(`(?m)^\s*fn\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)(\s*->\s*([A-Za-z_][A-Za-z0-9_:<>,\[\]\(\) ]*))?\s*;`)

type emittedFn struct {
	Name      string     `json:"fn"`
	Inputs    [][]string `json:"inputs"`
	Output    []string   `json:"output"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <markdown-file> [--sep N]\n", os.Args[0])
		os.Exit(1)
	}

	mdPath := os.Args[1]
	sep := 41 // default to SEP-41
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--sep" && i+1 < len(os.Args) {
			var err error
			sep, err = strconv.Atoi(os.Args[i+1])
			if err != nil || sep < 1 || sep > 999999 {
				fmt.Fprintf(os.Stderr, "invalid --sep value: %s\n", os.Args[i+1])
				os.Exit(1)
			}
			i++
		}
	}

	// Read the markdown file.
	f, err := os.Open(mdPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", mdPath, err)
		os.Exit(1)
	}
	defer f.Close()

	var builder strings.Builder
	scanner := bufio.NewScanner(f)
	inRust := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "```rust") || strings.HasPrefix(line, "```rust\n") {
			inRust = true
			continue
		}
		if inRust && (strings.HasPrefix(line, "```") || strings.HasPrefix(line, "```\n")) {
			inRust = false
			continue
		}
		if inRust {
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}

	content := builder.String()

	// Find the trait block: look for "pub trait" followed by name and {}.
	traitStart := strings.Index(content, "pub trait ")
	if traitStart < 0 {
		fmt.Fprintf(os.Stderr, "no pub trait block found in %s\n", mdPath)
		os.Exit(1)
	}

	// Find the opening { of the trait.
	traitOpen := strings.Index(content[traitStart:], "{")
	if traitOpen < 0 {
		fmt.Fprintf(os.Stderr, "no trait body found in %s\n", mdPath)
		os.Exit(1)
	}
	traitBody := content[traitStart+traitOpen+1 : len(content)-1] // exclude final }

	// Find all fn declarations within the trait body.
	var funcs []emittedFn
	fnMatches := fnPattern.FindAllStringSubmatch(traitBody, -1)
	for _, m := range fnMatches {
		fnName := m[1]
		paramsStr := m[2]
		// m[3] is the optional "-> return_type" part; m[4] is the return type alone.
		returnType := m[4]
		outputType := ""
		if returnType != "" {
			outputType = strings.TrimSpace(returnType)
		} else {
			// No explicit return type (void function); use empty output.
			outputType = ""
		}

		// Parse parameters, skipping `env: Env`.
		var inputs [][]string

		// Split parameters by comma, respecting nested brackets.
		params := splitParams(paramsStr)
		for _, param := range params {
			param = strings.TrimSpace(param)
			if param == "" {
				continue
			}
			// Skip `env: Env` parameter.
			if strings.HasPrefix(strings.ToLower(param), "env:") {
				continue
			}
			// Extract the type from "name: Type" or just "Type".
			// The format in Rust traits is "name: Type" within the params.
			typePart := extractTypeFromParam(param)
			if typePart == "" {
				continue
			}
			// Map to canonical name.
			canonical := mapType(typePart)
			inputs = append(inputs, []string{canonical})
		}

		// Process output type.
		output := parseOutputType(outputType)

		funcs = append(funcs, emittedFn{
			Name:      fnName,
			Inputs:    inputs,
			Output:    output,
		})
	}

	if len(funcs) == 0 {
		fmt.Fprintf(os.Stderr, "no fn declarations found in trait block of %s\n", mdPath)
		os.Exit(1)
	}

	// Build the rule file.
	rulesetVersion := fmt.Sprintf("generated-placeholder-%d", sep)
	ruleFile := map[string]interface{}{
		"sep":               sep,
		"ruleset_version":   rulesetVersion,
		"source":            fmt.Sprintf("generated from %s trait", mdPath),
		"required":          funcsToJSON(funcs),
		"optional":          []interface{}{},
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(ruleFile)
}

func splitParams(s string) []string {
	var result []string
	var current strings.Builder
	inAngle := false
	for _, r := range s {
		switch r {
		case '<':
			inAngle = true
			current.WriteRune(r)
		case '>':
			inAngle = false
			current.WriteRune(r)
		case '[':
			if !inAngle {
				current.WriteRune(r)
			} else {
				current.WriteRune(r)
			}
		case ']':
			if !inAngle {
				current.WriteRune(r)
			} else {
				current.WriteRune(r)
			}
		case '(':
			current.WriteRune(r)
		case ')':
			current.WriteRune(r)
		case ',':
			if !inAngle {
				result = append(result, current.String())
				current.Reset()
				continue
			}
			current.WriteRune(r)
		default:
			current.WriteRune(r)
		}
	}
	result = append(result, current.String())
	return result
}

func extractTypeFromParam(param string) string {
	// Param format: "from: Address" or just "Address"
	// Split on ":" and take the type part.
	parts := strings.SplitN(param, ":", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	// No colon - the whole thing might be a type name.
	return strings.TrimSpace(parts[0])
}

func mapType(typeName string) string {
	// Try exact match first.
	if canonical, ok := canonicalType[typeName]; ok {
		return canonical
	}
	// Try without angle brackets for generic types like Option<Address>
	// For now, just return the raw name; the human reviewer will check it.
	return typeName
}

func parseOutputType(outputType string) []string {
	outputType = strings.TrimSpace(outputType)
	outputType = strings.TrimSuffix(outputType, ";")
	outputType = strings.Trim(outputType, " ")

	if outputType == "" || outputType == "()" {
		return []string{}
	}

	// Handle simple types.
	if canonical, ok := canonicalType[outputType]; ok {
		return []string{canonical}
	}

	// Handle generic types like Option<Address>, Vec<Address>
	// For now, just return the raw type name.
	if outputType == "" {
		return []string{}
	}
	return []string{outputType}
}

func funcsToJSON(funcs []emittedFn) []interface{} {
	var result []interface{}
	for _, f := range funcs {
		inputs := f.Inputs
		if inputs == nil {
			inputs = [][]string{}
		}
		item := map[string]interface{}{
			"fn":      f.Name,
			"inputs":  inputs,
			"output":  f.Output,
		}
		result = append(result, item)
	}
	return result
}