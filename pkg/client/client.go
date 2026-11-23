// Package client provides a typed Go client for the sep47idx /v1 HTTP API.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a typed HTTP client for the sep47idx API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	network    string
}

// Option configures the Client.
type Option func(*Client)

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// WithTimeout sets the request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if c.httpClient == nil {
			c.httpClient = &http.Client{Timeout: d}
		} else {
			c.httpClient.Timeout = d
		}
	}
}

// WithNetwork sets the default network for multi-network servers.
func WithNetwork(n string) Option {
	return func(c *Client) {
		c.network = n
	}
}

// NewClient creates a new API client.
// baseURL should be the root URL of the server, e.g. "http://localhost:8080".
func NewClient(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// doRequest executes an HTTP request and decodes the JSON response.
// For non-2xx responses, it returns an APIError.
func (c *Client) doRequest(ctx context.Context, method, path string, query url.Values, result any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	if c.network != "" {
		req.Header.Set("Accept", "application/json")
		q := req.URL.Query()
		q.Set("network", c.network)
		req.URL.RawQuery = q.Encode()
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		var errResp ErrorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Code != "" {
			return &errResp.Error
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// --- /v1/contracts ---

// ContractsFilter holds filter parameters for the contracts endpoint.
type ContractsFilter struct {
	Implements    []int  // AND list of SEP numbers
	ImplementsAny []int  // OR list of SEP numbers
	Tier          string // declared, inferred, verified, protocol
	Kind          string // wasm, wasm_ref, sac
	Archived      *bool  // nil = live only, true = archived only, false = live only
	ExecRefOwner  string // filter by executable reference owner contract ID
	Limit         int    // page size (1-500)
	Cursor        string // opaque cursor from previous page
}

// ContractsPage is the response from the contracts endpoint.
type ContractsPage Page

// Contracts returns a single page of contracts matching the filter.
func (c *Client) Contracts(ctx context.Context, f ContractsFilter) (*ContractsPage, error) {
	q := url.Values{}
	if len(f.Implements) > 0 {
		q.Set("implements", intsToCSV(f.Implements))
	}
	if len(f.ImplementsAny) > 0 {
		q.Set("implements_any", intsToCSV(f.ImplementsAny))
	}
	if f.Tier != "" {
		q.Set("tier", f.Tier)
	}
	if f.Kind != "" {
		q.Set("kind", f.Kind)
	}
	if f.Archived != nil {
		if *f.Archived {
			q.Set("archived", "true")
		} else {
			q.Set("archived", "false")
		}
	}
	if f.ExecRefOwner != "" {
		q.Set("exec_ref_owner", f.ExecRefOwner)
	}
	if f.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", f.Limit))
	}
	if f.Cursor != "" {
		q.Set("cursor", f.Cursor)
	}

	var page ContractsPage
	if err := c.doRequest(ctx, "GET", "/v1/contracts", q, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// ContractsIterator returns an iterator that follows cursors until done.
// The iterator stops on context cancellation or when there are no more pages.
func (c *Client) ContractsIterator(ctx context.Context, f ContractsFilter) *ContractsIterator {
	// Copy the filter to avoid mutating the original.
	filter := f
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	return &ContractsIterator{
		client: c,
		ctx:    ctx,
		filter: filter,
	}
}

// ContractsIterator iterates over all pages of contracts.
type ContractsIterator struct {
	client    *Client
	ctx       context.Context
	filter    ContractsFilter
	page      *ContractsPage
	err       error
	exhausted bool
}

// Next advances to the next page and returns true if there are contracts.
func (it *ContractsIterator) Next() bool {
	if it.err != nil || it.exhausted {
		return false
	}
	if it.page != nil && it.page.NextCursor == nil {
		it.exhausted = true
		return false
	}
	if it.page != nil && it.page.NextCursor != nil {
		it.filter.Cursor = *it.page.NextCursor
	}
	page, err := it.client.Contracts(it.ctx, it.filter)
	if err != nil {
		it.err = err
		return false
	}
	it.page = page
	if len(page.Contracts) == 0 {
		it.exhausted = true
		return false
	}
	return true
}

// Page returns the current page.
func (it *ContractsIterator) Page() *ContractsPage {
	return it.page
}

// Contracts returns the contracts from the current page.
func (it *ContractsIterator) Contracts() []ContractSummary {
	if it.page == nil {
		return nil
	}
	return it.page.Contracts
}

// Err returns any error that occurred during iteration.
func (it *ContractsIterator) Err() error {
	return it.err
}

// --- /v1/contracts?ids=... (bulk lookup) ---

// LookupContracts returns summaries for a list of contract IDs.
func (c *Client) LookupContracts(ctx context.Context, ids []string) (*Lookup, error) {
	if len(ids) == 0 {
		return nil, errors.New("ids list cannot be empty")
	}
	q := url.Values{}
	q.Set("ids", strings.Join(ids, ","))
	var lookup Lookup
	if err := c.doRequest(ctx, "GET", "/v1/contracts", q, &lookup); err != nil {
		return nil, err
	}
	return &lookup, nil
}

// --- /v1/contracts/{id} ---

// GetContract returns the detail for a single contract.
func (c *Client) GetContract(ctx context.Context, id string) (*ContractDetail, error) {
	var detail ContractDetail
	if err := c.doRequest(ctx, "GET", "/v1/contracts/"+id, nil, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

// --- /v1/contracts/{id}/history ---

// HistoryPage is the response from the history endpoint.
type HistoryPage HistoryDetail

// GetHistory returns a single page of contract history.
func (c *Client) GetHistory(ctx context.Context, id string, limit int, cursor string) (*HistoryPage, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var page HistoryPage
	if err := c.doRequest(ctx, "GET", "/v1/contracts/"+id+"/history", q, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// HistoryIterator returns an iterator for contract history pages.
func (c *Client) HistoryIterator(ctx context.Context, id string, limit int) *HistoryIterator {
	if limit == 0 {
		limit = 50
	}
	return &HistoryIterator{
		client: c,
		ctx:    ctx,
		id:     id,
		limit:  limit,
	}
}

// HistoryIterator iterates over all pages of contract history.
type HistoryIterator struct {
	client    *Client
	ctx       context.Context
	id        string
	limit     int
	cursor    string
	page      *HistoryPage
	err       error
	exhausted bool
}

// Next advances to the next page and returns true if there are versions.
func (it *HistoryIterator) Next() bool {
	if it.err != nil || it.exhausted {
		return false
	}
	if it.page != nil && it.page.NextCursor == nil {
		it.exhausted = true
		return false
	}
	if it.page != nil && it.page.NextCursor != nil {
		it.cursor = *it.page.NextCursor
	}
	page, err := it.client.GetHistory(it.ctx, it.id, it.limit, it.cursor)
	if err != nil {
		it.err = err
		return false
	}
	it.page = page
	if len(page.History) == 0 {
		it.exhausted = true
		return false
	}
	return true
}

// Page returns the current page.
func (it *HistoryIterator) Page() *HistoryPage {
	return it.page
}

// History returns the history versions from the current page.
func (it *HistoryIterator) History() []HistoryVersion {
	if it.page == nil {
		return nil
	}
	return it.page.History
}

// Err returns any error that occurred during iteration.
func (it *HistoryIterator) Err() error {
	return it.err
}

// --- /v1/wasm/{hash} ---

// WasmFilter holds filter parameters for the wasm endpoint.
type WasmFilter struct {
	Limit  int    // page size (1-500)
	Cursor string // opaque cursor from previous page
}

// GetWasm returns parsed Wasm metadata and a page of contracts using it.
func (c *Client) GetWasm(ctx context.Context, hash string, f WasmFilter) (*WasmDetail, error) {
	q := url.Values{}
	if f.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", f.Limit))
	}
	if f.Cursor != "" {
		q.Set("cursor", f.Cursor)
	}
	var detail WasmDetail
	if err := c.doRequest(ctx, "GET", "/v1/wasm/"+hash, q, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

// --- /v1/seps ---

// SEPsFilter holds filter parameters for the seps endpoint.
type SEPsFilter struct {
	Limit  int    // page size (1-500)
	Cursor string // opaque cursor from previous page
}

// GetSEPs returns a single page of SEP counts.
func (c *Client) GetSEPs(ctx context.Context, f SEPsFilter) (*SEPPage, error) {
	q := url.Values{}
	if f.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", f.Limit))
	}
	if f.Cursor != "" {
		q.Set("cursor", f.Cursor)
	}
	var page SEPPage
	if err := c.doRequest(ctx, "GET", "/v1/seps", q, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// SEPsIterator returns an iterator for SEP pages.
func (c *Client) SEPsIterator(ctx context.Context, f SEPsFilter) *SEPsIterator {
	if f.Limit == 0 {
		f.Limit = 50
	}
	return &SEPsIterator{
		client: c,
		ctx:    ctx,
		filter: f,
	}
}

// SEPsIterator iterates over all pages of SEP counts.
type SEPsIterator struct {
	client    *Client
	ctx       context.Context
	filter    SEPsFilter
	page      *SEPPage
	err       error
	exhausted bool
}

// Next advances to the next page and returns true if there are SEPs.
func (it *SEPsIterator) Next() bool {
	if it.err != nil || it.exhausted {
		return false
	}
	if it.page != nil && it.page.NextCursor == nil {
		it.exhausted = true
		return false
	}
	if it.page != nil && it.page.NextCursor != nil {
		it.filter.Cursor = *it.page.NextCursor
	}
	page, err := it.client.GetSEPs(it.ctx, it.filter)
	if err != nil {
		it.err = err
		return false
	}
	it.page = page
	if len(page.SEPs) == 0 {
		it.exhausted = true
		return false
	}
	return true
}

// Page returns the current page.
func (it *SEPsIterator) Page() *SEPPage {
	return it.page
}

// SEPs returns the SEP counts from the current page.
func (it *SEPsIterator) SEPs() []SEPCounts {
	if it.page == nil {
		return nil
	}
	return it.page.SEPs
}

// Err returns any error that occurred during iteration.
func (it *SEPsIterator) Err() error {
	return it.err
}

// --- /v1/stats ---

// GetStats returns stored statistics.
func (c *Client) GetStats(ctx context.Context) (*Statistics, error) {
	var stats Statistics
	if err := c.doRequest(ctx, "GET", "/v1/stats", nil, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// --- /v1/stats/toolchains ---

// GetToolchains returns toolchain statistics.
func (c *Client) GetToolchains(ctx context.Context) (*ToolchainStats, error) {
	var stats ToolchainStats
	if err := c.doRequest(ctx, "GET", "/v1/stats/toolchains", nil, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// --- /v1/health ---

// GetHealth returns the health check response.
func (c *Client) GetHealth(ctx context.Context) (*HealthResponse, error) {
	var health HealthResponse
	if err := c.doRequest(ctx, "GET", "/v1/health", nil, &health); err != nil {
		return nil, err
	}
	return &health, nil
}

// --- /v1/events/upgrades (SSE) ---

// UpgradesStream streams contract version upgrade events via SSE.
// The context controls the lifetime of the stream. Each event is sent
// to the handler function. The handler should return quickly to avoid
// blocking the stream. If the handler returns an error, the stream stops.
func (c *Client) UpgradesStream(ctx context.Context, since uint32, handler func(VersionChange) error) error {
	q := url.Values{}
	if since > 0 {
		q.Set("since", fmt.Sprintf("%d", since))
	}
	u := c.baseURL + "/v1/events/upgrades"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.network != "" {
		req.Header.Set("network", c.network)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		var errResp ErrorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Code != "" {
			return &errResp.Error
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Parse SSE events.
	return parseSSE(resp.Body, handler)
}

// parseSSE parses Server-Sent Events from the response body.
func parseSSE(body io.Reader, handler func(VersionChange) error) error {
	// Simple SSE parser: events are separated by blank lines.
	// Each event has fields like "id: 123", "data: {...}".
	var data strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			lines := strings.Split(chunk, "\n")
			for _, line := range lines {
				line = strings.TrimSuffix(line, "\r")
				if line == "" {
					// End of event - process it.
					if data.Len() > 0 {
						var change VersionChange
						if err := json.Unmarshal([]byte(data.String()), &change); err != nil {
							// Skip malformed events.
						} else if err := handler(change); err != nil {
							return err
						}
						data.Reset()
					}
					continue
				}
				if strings.HasPrefix(line, "id:") {
					_ = strings.TrimSpace(line[3:]) // event ID, not used but parsed for spec compliance
				} else if strings.HasPrefix(line, "data:") {
					if data.Len() > 0 {
						data.WriteString("\n")
					}
					data.WriteString(strings.TrimSpace(line[5:]))
				}
				// Ignore other fields like "retry:", "event:", etc.
			}
		}
		if err != nil {
			if err == io.EOF {
				// Process any pending event.
				if data.Len() > 0 {
					var change VersionChange
					if err := json.Unmarshal([]byte(data.String()), &change); err == nil {
						_ = handler(change)
					}
				}
				return nil
			}
			return err
		}
	}
}

// --- Helper functions ---

func intsToCSV(nums []int) string {
	if len(nums) == 0 {
		return ""
	}
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ",")
}
