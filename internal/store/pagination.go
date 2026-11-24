package store

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ErrInvalidCursor is a cursor that does not decode, fails its checksum or
// names an invalid contract. It carries no detail so a caller cannot probe
// which part was wrong. It is an ErrBadQuery.
var ErrInvalidCursor = fmt.Errorf("%w: invalid cursor", ErrBadQuery)

// ErrStaleCursor is a well-formed cursor issued under another cursor format,
// schema version or set of ruleset versions. The caller must restart paging.
// It is an ErrBadQuery.
var ErrStaleCursor = fmt.Errorf("%w: cursor was issued by another version", ErrBadQuery)

// cursorVersion is the current cursor format. Version 1 was an unversioned
// contract ID wrapper; it is refused as stale.
const cursorVersion = 2

// contractCursor is the keyset position after one contract. S and R tie it to
// the schema and rulesets it was issued under, and C is a checksum over every
// other field so that an edited or corrupted cursor is detected. The checksum
// is not a secret MAC: a cursor holds only a public contract ID, so forging
// one reveals nothing and the check only guards against accidents and edits.
type contractCursor struct {
	Version int    `json:"v"`
	Schema  int    `json:"s,omitempty"`
	Rules   string `json:"r,omitempty"`
	ID      string `json:"id"`
	Sum     string `json:"c,omitempty"`
}

// rulesetStamp is a short, order-independent digest of the loaded ruleset
// versions.
func rulesetStamp(rulesets map[int]string) string {
	seps := make([]int, 0, len(rulesets))
	for sep := range rulesets {
		seps = append(seps, sep)
	}
	sort.Ints(seps)
	var b strings.Builder
	for _, sep := range seps {
		b.WriteString(strconv.Itoa(sep) + "=" + rulesets[sep] + ";")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:4])
}

func (c contractCursor) checksum() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("soroindex-cursor\x00%d\x00%d\x00%s\x00%s", c.Version, c.Schema, c.Rules, c.ID)))
	return hex.EncodeToString(sum[:8])
}

func contractPageInput(limit int, cursor string, rulesets map[int]string) (int, string, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 500 {
		return 0, "", ErrBadQuery
	}
	if cursor == "" {
		return limit, "", nil
	}
	id, err := decodeContractCursor(cursor, rulesets)
	if err != nil {
		return 0, "", err
	}
	return limit, id, nil
}

// decodeContractCursor returns the contract ID a cursor continues after. A
// version 1 cursor, or one issued under another schema or ruleset version, is
// ErrStaleCursor; anything else wrong is ErrInvalidCursor. The checksum is
// verified before the schema and rulesets are compared, so an edited stamp is
// invalid rather than stale.
func decodeContractCursor(cursor string, rulesets map[int]string) (string, error) {
	if len(cursor) > 256 {
		return "", ErrInvalidCursor
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", ErrInvalidCursor
	}
	var c contractCursor
	if err = json.Unmarshal(b, &c); err != nil || ValidateContractID(c.ID) != nil {
		return "", ErrInvalidCursor
	}
	if c.Version == 1 {
		return "", ErrStaleCursor
	}
	if c.Version != cursorVersion || c.Sum != c.checksum() {
		return "", ErrInvalidCursor
	}
	schema, err := SchemaVersion()
	if err != nil {
		return "", errors.New("cursor: schema version unavailable")
	}
	if c.Schema != schema || c.Rules != rulesetStamp(rulesets) {
		return "", ErrStaleCursor
	}
	return c.ID, nil
}

func encodeContractCursor(id string, rulesets map[int]string) string {
	schema, _ := SchemaVersion() // embedded migrations; a failure surfaces in Open
	c := contractCursor{Version: cursorVersion, Schema: schema, Rules: rulesetStamp(rulesets), ID: id}
	c.Sum = c.checksum()
	b, _ := json.Marshal(c) // fixed struct of strings and ints cannot fail to encode
	return base64.RawURLEncoding.EncodeToString(b)
}
