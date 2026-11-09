package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserClientHTMLStructure(t *testing.T) {
	// Root of repo is two levels up from internal/api
	path := filepath.Join("..", "..", "examples", "browser_client.html")
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read examples/browser_client.html: %v", err)
	}
	content := string(contentBytes)

	// 1. Acceptance Criteria: Keep verified caveats and unresolved/archived states visible
	requiredStateTokens := []string{
		"badge-declared",
		"badge-inferred",
		"badge-verified",
		"badge-protocol",
		"badge-archived",
		"ARCHIVED",
		"ACTIVE",
		"Verification Caveat",
		"unresolved / not verified",
	}
	for _, tok := range requiredStateTokens {
		if !strings.Contains(content, tok) {
			t.Errorf("expected browser_client.html to contain evidence tier/state token %q", tok)
		}
	}

	// 2. Acceptance Criteria: Follow opaque cursors and handle cancellation/rate limits
	requiredControlTokens := []string{
		"AbortController",
		"currentAbortController.abort()",
		"Retry-After",
		"next_cursor",
		"cursorStack",
		"status-rate-limited",
		"429",
	}
	for _, tok := range requiredControlTokens {
		if !strings.Contains(content, tok) {
			t.Errorf("expected browser_client.html to contain control/pagination token %q", tok)
		}
	}

	// 3. Acceptance Criteria: Document a tested local workflow without presenting it as hosted production API
	requiredNoticeTokens := []string{
		"Tested Local Workflow Notice",
		"not a public hosted production service",
		"http://127.0.0.1:8080",
	}
	for _, tok := range requiredNoticeTokens {
		if !strings.Contains(content, tok) {
			t.Errorf("expected browser_client.html to contain local workflow disclaimer %q", tok)
		}
	}
}

func TestBrowserClientCORSAndCatalogIntegration(t *testing.T) {
	s := catalogServer(t)
	ts := httptest.NewServer(s)
	defer ts.Close()

	client := ts.Client()

	// 1. Check Preflight OPTIONS for GET CORS
	req, err := http.NewRequest(http.MethodOptions, ts.URL+"/v1/contracts", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://127.0.0.1:8000")
	req.Header.Set("Access-Control-Request-Method", "GET")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 No Content for OPTIONS, got %d", res.StatusCode)
	}
	if origin := res.Header.Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("expected Access-Control-Allow-Origin '*', got %q", origin)
	}
	if methods := res.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(methods, "GET") {
		t.Errorf("expected Access-Control-Allow-Methods to contain GET, got %q", methods)
	}

	// 2. Query /v1/contracts with browser client Origin header
	reqGet, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/contracts?tier=declared&limit=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqGet.Header.Set("Origin", "http://127.0.0.1:8000")
	reqGet.Header.Set("Accept", "application/json")
	resGet, err := client.Do(reqGet)
	if err != nil {
		t.Fatal(err)
	}
	defer resGet.Body.Close()

	if resGet.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /v1/contracts, got %d", resGet.StatusCode)
	}
	if origin := resGet.Header.Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("expected Access-Control-Allow-Origin '*', got %q", origin)
	}

	var page struct {
		Contracts []struct {
			ContractID string `json:"contract_id"`
			Kind       string `json:"kind"`
			Archived   bool   `json:"archived"`
		} `json:"contracts"`
		Tier       string  `json:"tier"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.NewDecoder(resGet.Body).Decode(&page); err != nil {
		t.Fatalf("cannot decode contracts page response: %v", err)
	}
	if page.Tier != "declared" {
		t.Errorf("expected tier 'declared', got %q", page.Tier)
	}
	if len(page.Contracts) == 0 {
		t.Fatal("expected at least one contract returned in fixture server")
	}

	firstID := page.Contracts[0].ContractID

	// 3. Query /v1/contracts/{id} detail to inspect evidence tiers
	resDetail, err := client.Get(ts.URL + "/v1/contracts/" + firstID)
	if err != nil {
		t.Fatal(err)
	}
	defer resDetail.Body.Close()

	if resDetail.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /v1/contracts/{id}, got %d", resDetail.StatusCode)
	}

	var detail struct {
		ContractID string `json:"contract_id"`
		Archived   bool   `json:"archived"`
		Claims     []struct {
			SEP      int  `json:"sep"`
			Declared bool `json:"declared"`
			Inferred *struct {
				Status string `json:"status"`
			} `json:"inferred"`
			Verified *struct {
				Status string `json:"status"`
				Caveat string `json:"caveat"`
			} `json:"verified"`
			Protocol bool `json:"protocol"`
		} `json:"claims"`
	}
	if err := json.NewDecoder(resDetail.Body).Decode(&detail); err != nil {
		t.Fatalf("cannot decode contract detail: %v", err)
	}
	if detail.ContractID != firstID {
		t.Errorf("expected detail for %q, got %q", firstID, detail.ContractID)
	}
}
