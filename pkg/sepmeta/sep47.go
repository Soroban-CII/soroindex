package sepmeta

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Mode selects how ParseSEP47 treats tokens that are recognizable but do not
// follow SEP-47's format.
type Mode int

const (
	// Lenient accepts "041" and "SEP-41" as 41 and records an anomaly. It is
	// the default because the declared tier is informative and anomalies are
	// shown next to it.
	Lenient Mode = iota
	// Strict accepts only canonical decimal numbers.
	Strict
)

// Anomaly codes. They are stored in the database and returned by the API,
// so they are part of the public surface; do not rename them.
const (
	AnomalyLeadingZero   = "leading_zero"
	AnomalyPrefixedToken = "prefixed_token"
	AnomalyNonNumeric    = "non_numeric"
	AnomalyEmptyToken    = "empty_token"
	AnomalyOversized     = "oversized"
)

const (
	// SEPMetaKey is the meta key SEP-47 defines. It is case-sensitive.
	SEPMetaKey = "sep"
	// maxValueBytes: a sep value longer than this is not split at all.
	maxValueBytes = 4096
	// maxDigits: no SEP number is longer than this.
	maxDigits = 6
	// maxRawBytes caps the raw text kept for an oversized value, so hostile
	// meta cannot be copied in full into every row and response.
	maxRawBytes = 64
)

// Token is one comma-separated item from a sep meta value.
type Token struct {
	// Raw is the token as written, after trimming ASCII whitespace. For an
	// oversized value it is the value's first 64 bytes followed by "…".
	Raw string `json:"raw"`
	// SEP is the number the token denotes, or 0 if it denotes none. It is
	// set even when Strict mode does not accept the token.
	SEP int `json:"sep"`
	// Anomaly is "" for a canonical token, else one of the Anomaly* codes.
	Anomaly string `json:"anomaly"`
	// Accepted reports whether the token contributed SEP to SEPClaims.SEPs
	// in the mode used.
	Accepted bool `json:"accepted"`
}

// SEPClaims is what a contract's meta declares under SEP-47.
type SEPClaims struct {
	// SEPs are the accepted SEP numbers, deduplicated, in first-seen order.
	SEPs []int `json:"seps"`
	// Tokens records every token, accepted or not, so nothing is dropped
	// silently.
	Tokens []Token `json:"tokens"`
	// EntryCount is how many meta entries had the key "sep". More than one
	// is normal: SEP-47 joins repeated entries with commas.
	EntryCount int `json:"entry_count"`
}

// ParseSEP47 extracts SEP-47 declarations from meta entries. Entries whose
// key is exactly "sep" are joined with commas, as SEP-47 specifies, then
// split into tokens and classified (CLAUDE.md §5.2).
func ParseSEP47(entries []MetaEntry, mode Mode) SEPClaims {
	c := SEPClaims{SEPs: []int{}, Tokens: []Token{}}
	seen := map[int]bool{}
	for _, e := range entries {
		if e.Key != SEPMetaKey {
			continue
		}
		c.EntryCount++
		if len(e.Value) > maxValueBytes {
			c.Tokens = append(c.Tokens, Token{Raw: capRaw(e.Value), Anomaly: AnomalyOversized})
			continue
		}
		for _, part := range strings.Split(e.Value, ",") {
			tok := classify(strings.Trim(part, " \t\n\r\v\f"), mode)
			if tok.Accepted && !seen[tok.SEP] {
				seen[tok.SEP] = true
				c.SEPs = append(c.SEPs, tok.SEP)
			}
			c.Tokens = append(c.Tokens, tok)
		}
	}
	return c
}

func classify(raw string, mode Mode) Token {
	t := Token{Raw: raw}
	if raw == "" {
		t.Anomaly = AnomalyEmptyToken
		return t
	}
	digits, prefixed := stripSEPPrefix(raw)
	if !isASCIIDigits(digits) {
		t.Anomaly = AnomalyNonNumeric
		return t
	}
	if len(digits) > maxDigits {
		t.Anomaly = AnomalyOversized
		return t
	}
	n, err := strconv.Atoi(digits)
	if err != nil { // unreachable: at most 6 ASCII digits always parse
		t.Anomaly = AnomalyNonNumeric
		return t
	}
	t.SEP = n
	switch {
	case prefixed:
		t.Anomaly = AnomalyPrefixedToken
	case len(digits) > 1 && digits[0] == '0':
		t.Anomaly = AnomalyLeadingZero
	}
	t.Accepted = t.Anomaly == "" || mode == Lenient
	return t
}

// stripSEPPrefix removes a leading "SEP-" or "SEP" in any letter case. Only
// these two spellings are recognized; anything else is left for the digit
// check to reject.
func stripSEPPrefix(s string) (rest string, ok bool) {
	if len(s) < 3 || !strings.EqualFold(s[:3], "sep") {
		return s, false
	}
	rest = strings.TrimPrefix(s[3:], "-")
	return rest, true
}

func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func capRaw(s string) string {
	if len(s) <= maxRawBytes {
		return s
	}
	cut := maxRawBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut-- // do not split a multi-byte character
	}
	return s[:cut] + "…"
}
