package store

import (
	"encoding/base64"
	"encoding/json"
)

type contractCursor struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
}

func contractPageInput(limit int, cursor string) (int, string, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 500 {
		return 0, "", ErrBadQuery
	}
	if cursor == "" {
		return limit, "", nil
	}
	if len(cursor) > 256 {
		return 0, "", ErrBadQuery
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", ErrBadQuery
	}
	var c contractCursor
	if err = json.Unmarshal(b, &c); err != nil || c.Version != 1 || ValidateContractID(c.ID) != nil {
		return 0, "", ErrBadQuery
	}
	return limit, c.ID, nil
}

func encodeContractCursor(id string) string {
	b, _ := json.Marshal(contractCursor{1, id})
	return base64.RawURLEncoding.EncodeToString(b)
}
