package api

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPythonClientAgainstFixtureServer(t *testing.T) {
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		pythonPath, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("python not available in test environment")
	}

	server := catalogServer(t)
	ts := httptest.NewServer(server)
	defer ts.Close()

	cmd := exec.Command(pythonPath, "../../examples/python_client.py", ts.URL)
	// Bypass any proxy for local loopback test server
	var cleanEnv []string
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "HTTP_PROXY=") && !strings.HasPrefix(env, "HTTPS_PROXY=") && !strings.HasPrefix(env, "http_proxy=") && !strings.HasPrefix(env, "https_proxy=") {
			cleanEnv = append(cleanEnv, env)
		}
	}
	cleanEnv = append(cleanEnv, "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost")
	cmd.Env = cleanEnv

	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		t.Fatalf("python client failed: %v\noutput:\n%s", err, output)
	}
	t.Logf("Python Client Real Output:\n%s", output)

	// Verify real output assertions
	checks := []string{
		"Connecting to soroindex at " + ts.URL,
		"Health: status=ok network=testnet",
		"--- 1. Declared Tier (self-reported in Wasm meta) ---",
		"Found 2 contracts declaring SEP-41:",
		"--- 2. Inferred Tier (interface rules matched from Wasm bytecode) ---",
		"Found 3 contracts with inferred match for SEP-41:",
		"--- 3. Protocol Tier (built-in Stellar Asset Contracts) ---",
		"Found 1 protocol SACs implementing SEP-41:",
		"--- 4. Empty Result vs Error Distinctions ---",
		"Empty query returned 200 with 0 items (not an error): 0 items",
		"Handled 400 Bad Request successfully: code=bad_request",
		"Handled 404 Not Found successfully: code=not_found",
	}

	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("missing expected output %q in:\n%s", check, output)
		}
	}
}
