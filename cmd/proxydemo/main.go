package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"asare_poc/pkg/ledger"
	"asare_poc/pkg/mockservices"
	"asare_poc/pkg/proxy"
	"asare_poc/pkg/registry"
)

// proxydemo proves the transparent-proxy pattern:
// the agent calls the PROXY (port 9999) exactly like it would call the real
// API (port 8888). No SDK changes. The proxy logs every write to the WAL and
// auto-rolls-back when a later step fails.
//
// A special fail-point header (X-Fail-Step) lets the mock server fail a
// specific step to simulate an upstream outage mid-workflow.
func main() {
	log.SetFlags(log.Ltime)
	baseDir, _ := os.Getwd()
	walFile := filepath.Join(baseDir, "asare_wal.json")
	mockDBFile := filepath.Join(baseDir, "mock_db.json")

	_ = os.Remove(walFile)
	_ = os.Remove(mockDBFile)

	mockDB := mockservices.NewMockDB(mockDBFile)
	mockServer := mockservices.SetupMockServer(mockDB)
	go mockServer.ListenAndServe()
	time.Sleep(200 * time.Millisecond)
	defer mockServer.Close()

	wal, err := ledger.NewWAL(walFile)
	if err != nil {
		log.Fatalf("WAL init failed: %v", err)
	}
	reg, err := registry.Load(filepath.Join(baseDir, "inverse_registry.yaml"))
	if err != nil {
		log.Fatalf("Registry load failed: %v", err)
	}

	const proxyAddr = "127.0.0.1:9999"
	const upstream = "http://127.0.0.1:8888"

	execID := fmt.Sprintf("exec_proxy_%d", time.Now().UnixNano()/1000000)
	p := proxy.New(upstream, wal, reg, execID)
	proxyServer := &http.Server{Addr: proxyAddr, Handler: p.Handler()}
	go proxyServer.ListenAndServe()
	time.Sleep(200 * time.Millisecond)
	defer proxyServer.Close()

	fmt.Println("==================================================================")
	fmt.Println("       ASARE TRANSPARENT PROXY DEMO (no agent SDK changes)        ")
	fmt.Println("==================================================================")
	log.Printf("[DEMO] Agent -> proxy %s -> upstream %s (exec %s)", proxyAddr, upstream, execID)

	agentCall := func(method, path string, body map[string]any, failStep string) (int, map[string]any) {
		bodyBytes, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, "http://"+proxyAddr+path, bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		if failStep != "" {
			req.Header.Set("X-Fail-Step", failStep)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("[DEMO] %s %s ERROR: %v", method, path, err)
			return 0, nil
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		log.Printf("[DEMO] %s %s -> %d %v", method, path, resp.StatusCode, out)
		return resp.StatusCode, out
	}

	// Step 1: charge via proxy (succeeds)
	agentCall(http.MethodPost, "/mock/stripe/charge", map[string]any{"amount": 500}, "")

	// Step 2: create contact via proxy (succeeds)
	agentCall(http.MethodPost, "/mock/hubspot/contact", map[string]any{"email": "client@example.com"}, "")

	fmt.Println("\n--- SYSTEM STATE BEFORE FAILURE ---")
	mockservices.PrintSystemState(upstream)

	// Step 3: upstream outage mid-workflow (mock server returns 500)
	log.Printf("[DEMO] Injecting upstream failure on step 3 (Slack notify)...")
	agentCall(http.MethodPost, "/mock/stripe/charge", map[string]any{"amount": 999}, "fail")

	fmt.Println("\n--- WAL after failure (expect ROLLED_BACK steps) ---")
	for _, s := range wal.StepsForExecution(execID) {
		fmt.Printf("  [%s] step %d: %s\n", s.Status, s.StepIndex, s.Name)
	}

	fmt.Println("\n--- SYSTEM STATE AFTER AUTO-ROLLBACK ---")
	mockservices.PrintSystemState(upstream)

	fmt.Println("\n==================================================================")
	fmt.Println("       PROXY DEMO COMPLETE: ORPHAN STATE AUTO-ROLLED BACK         ")
	fmt.Println("==================================================================")
}
