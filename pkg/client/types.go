// Package client provides a typed Go client for the sep47idx /v1 HTTP API.
package client

import (
	"encoding/json"
)

// Reference identifies the CAP-85 executable owner and tag.
type Reference struct {
	Owner string `json:"owner"`
	Tag   string `json:"tag"`
}

// ContractSummary is the current executable of one contract.
type ContractSummary struct {
	ID       string     `json:"contract_id"`
	Kind     string     `json:"kind"`
	WasmHash *string    `json:"wasm_hash"`
	ExecRef  *Reference `json:"exec_ref"`
	Archived bool       `json:"archived"`
}

// Page uses an opaque cursor to continue after the last returned contract.
type Page struct {
	Contracts       []ContractSummary `json:"contracts"`
	NextCursor      *string           `json:"next_cursor"`
	ParserVersion   string            `json:"parser_version"`
	Tier            string            `json:"tier"`
	RulesetVersions map[int]string    `json:"ruleset_versions,omitempty"`
}

// ContractDetail implements the fixed contract response.
type ContractDetail struct {
	ContractID    string           `json:"contract_id"`
	Network       string           `json:"network"`
	Kind          string           `json:"kind"`
	WasmHash      *string          `json:"wasm_hash"`
	ExecRef       *Reference       `json:"exec_ref"`
	Archived      bool             `json:"archived"`
	Claims        []ClaimDetail    `json:"claims"`
	History       []VersionSummary `json:"history"`
	ParserVersion string           `json:"parser_version"`
	AsOfLedger    uint32           `json:"as_of_ledger"`
}

// InferredDetail exposes interface evidence without treating it as a declaration.
type InferredDetail struct {
	Status         string          `json:"status"`
	RulesetVersion string          `json:"ruleset_version"`
	Missing        json.RawMessage `json:"missing"`
	Mismatched     json.RawMessage `json:"mismatched"`
}

// VerificationDetail records a suite result and whether it belongs to old code.
type VerificationDetail struct {
	Verdict     string `json:"verdict"`
	Tool        string `json:"tool"`
	ToolVersion string `json:"tool_version"`
	RunAt       string `json:"run_at"`
	Stale       bool   `json:"stale"`
}

// ClaimDetail keeps the four trust tiers independent for one SEP.
type ClaimDetail struct {
	SEP      int                 `json:"sep"`
	Declared bool                `json:"declared"`
	Inferred *InferredDetail     `json:"inferred"`
	Verified *VerificationDetail `json:"verified"`
	Protocol bool                `json:"protocol"`
	Source   string              `json:"source"`
	Anomaly  *string             `json:"anomaly"`
}

// VersionSummary exposes half-open ledger ranges and executable references.
type VersionSummary struct {
	WasmHash   *string    `json:"wasm_hash"`
	FromLedger uint32     `json:"from_ledger"`
	ToLedger   *uint32    `json:"to_ledger"`
	ExecRef    *Reference `json:"exec_ref"`
}

// HistoryVersion includes the claims at each historical executable.
type HistoryVersion struct {
	VersionSummary
	Claims []ClaimDetail `json:"claims"`
}

// HistoryDetail is the complete contract history with provenance.
type HistoryDetail struct {
	ContractID    string           `json:"contract_id"`
	Network       string           `json:"network"`
	History       []HistoryVersion `json:"history"`
	ParserVersion string           `json:"parser_version"`
	AsOfLedger    uint32           `json:"as_of_ledger"`
	NextCursor    *string          `json:"next_cursor"`
}

// WasmClaim preserves the original declaration token and any anomaly.
type WasmClaim struct {
	SEP      int     `json:"sep"`
	Source   string  `json:"source"`
	RawToken string  `json:"raw_token"`
	Anomaly  *string `json:"anomaly"`
}

// WasmAnomaly is a token that produced no claim.
type WasmAnomaly struct {
	RawToken string `json:"raw_token"`
	Anomaly  string `json:"anomaly"`
}

// WasmMatch includes the ruleset and full interface evidence.
type WasmMatch struct {
	SEP             int             `json:"sep"`
	RulesetVersion  string          `json:"ruleset_version"`
	Status          string          `json:"status"`
	OKCount         int             `json:"ok_count"`
	Missing         json.RawMessage `json:"missing"`
	Mismatched      json.RawMessage `json:"mismatched"`
	MatchedVariants json.RawMessage `json:"matched_variants"`
}

// WasmDetail exposes stored parsing evidence and current users of the code.
type WasmDetail struct {
	Hash          string            `json:"wasm_hash"`
	Meta          json.RawMessage   `json:"meta"`
	Claims        []WasmClaim       `json:"claims"`
	Anomalies     []WasmAnomaly     `json:"anomalies"`
	Matches       []WasmMatch       `json:"interface_matches"`
	Contracts     []ContractSummary `json:"contracts"`
	NextCursor    *string           `json:"next_cursor"`
	ParseStatus   string            `json:"parse_status"`
	ParseError    *string           `json:"parse_error"`
	ParserVersion string            `json:"parser_version"`
	AsOfLedger    uint32            `json:"as_of_ledger"`
}

// SEPCounts counts distinct live contracts independently in each trust tier.
type SEPCounts struct {
	SEP      int `json:"sep"`
	Declared int `json:"declared"`
	Inferred int `json:"inferred"`
	Verified int `json:"verified"`
	Protocol int `json:"protocol"`
}

// SEPPage carries the versions used to compute inferred counts.
type SEPPage struct {
	SEPs            []SEPCounts    `json:"seps"`
	NextCursor      *string        `json:"next_cursor"`
	RulesetVersions map[int]string `json:"ruleset_versions"`
	ParserVersion   string         `json:"parser_version"`
	AsOfLedger      uint32         `json:"as_of_ledger"`
}

// Statistics exposes adoption and gap counts from the same database snapshot.
type Statistics struct {
	Network           string         `json:"network"`
	Adoption          Totals         `json:"adoption"`
	Anomalies         map[string]int `json:"anomalies"`
	ArchivedContracts int            `json:"archived_contracts"`
	TotalContracts    int            `json:"total_contracts"`
	LastLedger        uint32         `json:"last_ledger"`
	RulesetVersions   map[int]string `json:"ruleset_versions"`
	ParserVersion     string         `json:"parser_version"`
}

// Totals mirrors the internal Totals for the client.
type Totals struct {
	LiveSAC             int            `json:"live_sac"`
	LiveWasm            int            `json:"live_wasm"`
	LiveWasmRef         int            `json:"live_wasm_ref"`
	WasmRefUnresolved   int            `json:"wasm_ref_unresolved"`
	MeasuredContracts   int            `json:"measured_contracts"`
	UsedHashes          int            `json:"used_hashes"`
	DeclaringContracts  int            `json:"declaring_contracts"`
	DeclaringUsedHashes int            `json:"declaring_used_hashes"`
	SEP41ByContract     map[string]int `json:"sep41_by_contract"`
	SEP41ByUsedHash     map[string]int `json:"sep41_by_used_hash"`
	GapContracts        int            `json:"gap_contracts"`
	GapUsedHashes       int            `json:"gap_used_hashes"`
}

// ToolchainBucket counts one version of one toolchain key.
type ToolchainBucket struct {
	Version                 string  `json:"version"`
	Hashes                  int     `json:"hashes"`
	Contracts               int     `json:"contracts"`
	DeclaringHashes         int     `json:"declaring_hashes"`
	DeclaringContracts      int     `json:"declaring_contracts"`
	SEP41MatchHashes        int     `json:"sep41_match_hashes"`
	SEP41MatchContracts     int     `json:"sep41_match_contracts"`
	DeclaringPctByHash      float64 `json:"declaring_pct_by_hash"`
	DeclaringPctByContract  float64 `json:"declaring_pct_by_contract"`
	SEP41MatchPctByHash     float64 `json:"sep41_match_pct_by_hash"`
	SEP41MatchPctByContract float64 `json:"sep41_match_pct_by_contract"`
}

// ToolchainStats is the toolchain distribution over measured code.
type ToolchainStats struct {
	Network             string                       `json:"network"`
	MeasuredHashes      int                          `json:"measured_hashes"`
	MeasuredContracts   int                          `json:"measured_contracts"`
	Toolchains          map[string][]ToolchainBucket `json:"toolchains"`
	SEP41RulesetVersion *string                      `json:"sep41_ruleset_version"`
	ParserVersion       string                       `json:"parser_version"`
	AsOfLedger          uint32                       `json:"as_of_ledger"`
}

// HealthResponse is the health endpoint response.
type HealthResponse struct {
	Status       string `json:"status"`
	Network      string `json:"network"`
	LastLedger   uint32 `json:"last_ledger"`
	LatestLedger uint32 `json:"latest_ledger"`
	Lag          uint32 `json:"lag"`
}

// Lookup is the bulk answer for a list of contract IDs.
type Lookup struct {
	Contracts     []ContractSummary `json:"contracts"`
	NotFound      []string          `json:"not_found"`
	ParserVersion string            `json:"parser_version"`
	AsOfLedger    uint32            `json:"as_of_ledger"`
}

// VersionChange represents a contract version upgrade event for SSE streaming.
type VersionChange struct {
	ContractID   string     `json:"contract_id"`
	PreviousHash *string    `json:"previous_wasm_hash"`
	NewHash      *string    `json:"new_wasm_hash"`
	Ledger       uint32     `json:"ledger"`
	Kind         string     `json:"kind"`
	ExecRef      *Reference `json:"exec_ref"`
}

// APIError is the standard error shape returned by the API.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return e.Code + ": " + e.Message
}

// ErrorResponse wraps an APIError in the standard response shape.
type ErrorResponse struct {
	Error APIError `json:"error"`
}
