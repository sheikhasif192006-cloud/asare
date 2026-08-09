package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"asare_poc/pkg/agent"
	"asare_poc/pkg/compensator"
	"asare_poc/pkg/ledger"
	"asare_poc/pkg/mockservices"
	"asare_poc/pkg/registry"
)

func main() {
	crashMode := flag.Bool("crash", false, "Execute the onboarding workflow then crash at step 3")
	recoverMode := flag.Bool("recover", false, "Recover (rollback) any unfinished executions at startup")
	flag.Parse()

	log.SetFlags(log.Ltime)
	fmt.Println("==================================================================")
	fmt.Println("       ASARE: Agent State & Action Reconciliation Engine POC      ")
	fmt.Println("==================================================================")

	baseDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get current working directory: %v", err)
	}
	walFile := filepath.Join(baseDir, "asare_wal.json")
	mockDBFile := filepath.Join(baseDir, "mock_db.json")

	// Persistent on-disk mock state so a crashed run's side effects survive
	// into the next process for real recovery verification.
	mockDB := mockservices.NewMockDB(mockDBFile)
	mockServer := mockservices.SetupMockServer(mockDB)
	go func() {
		if err := mockServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Mock Server failed: %v", err)
		}
	}()
	time.Sleep(200 * time.Millisecond)

	wal, err := ledger.NewWAL(walFile)
	if err != nil {
		log.Fatalf("Failed to initialize WAL: %v", err)
	}

	// Load declarative inverse-action registry (compensation rules)
	reg, err := registry.Load(filepath.Join(baseDir, "inverse_registry.yaml"))
	if err != nil {
		log.Fatalf("Failed to load inverse registry: %v", err)
	}
	log.Printf("[ASARE] Loaded %d inverse-action rules from inverse_registry.yaml", len(reg.Rules))

	baseURL := "http://127.0.0.1:8888"

	// lookupInverse resolves a forward action to its compensating action via the registry.
	lookupInverse := func(method, url string) ledger.Action {
		inv, ok := reg.Lookup(method, url)
		if !ok {
			log.Fatalf("[ASARE] No inverse rule for %s %s", method, url)
		}
		if len(inv.URL) > 0 && inv.URL[0] == '/' {
			inv.URL = baseURL + inv.URL
		}
		return inv
	}

	// =========================================================================
	// RECOVERY MODE: detect unfinished executions and roll them back, then exit
	// =========================================================================
	if *recoverMode {
		unfinished := wal.FindUnfinishedExecutions()
		if len(unfinished) == 0 {
			log.Println("[ASARE RECOVERY] No unfinished executions found. Nothing to roll back.")
			mockServer.Close()
			return
		}
		fmt.Println("\n>>> [ASARE RECOVERY] Unfinished executions found at startup. Initiating rollback...")
		comp := compensator.NewCompensator(wal, baseURL)
		for _, execID := range unfinished {
			_, rbErr := comp.RollbackExecution(execID)
			if rbErr != nil {
				log.Printf("[ASARE RECOVERY ERROR] Rollback for %s failed: %v", execID, rbErr)
			} else {
				log.Printf("[ASARE RECOVERY] Rollback for %s completed.", execID)
			}
		}
		fmt.Println("\n>>> [ASARE RECOVERY] Final system state after rollback:")
		mockservices.PrintSystemState(baseURL)
		fmt.Println("\n==================================================================")
		fmt.Println("       ASARE RECOVERY COMPLETE: CRASHED RUN CLEANLY ROLLED BACK   ")
		fmt.Println("==================================================================")
		mockServer.Close()
		return
	}

	// =========================================================================
	// CRASH MODE (default): run the onboarding workflow, crash at step 3
	// =========================================================================
	_ = crashMode // default behavior executes the workflow; -crash flag is documentation
	execID := fmt.Sprintf("exec_onboard_%d", time.Now().UnixNano()/1000000)
	asareAgent := agent.NewASAREAgent(wal, execID)

	fmt.Println("\n>>> STARTING AGENT WORKFLOW [ID: " + execID + "]")
	fmt.Println("Goal: Onboard client by Charging Stripe ($500), Creating HubSpot Contact, then Sending Slack Msg.")

	// Step 1: Stripe Charge
	_, err = asareAgent.ExecuteStep(
		1,
		"Stripe $500 Payment Charge",
		ledger.Action{Method: "POST", URL: baseURL + "/mock/stripe/charge", Body: map[string]any{"amount": 500}},
		lookupInverse("POST", baseURL+"/mock/stripe/charge"),
	)
	if err != nil {
		log.Printf("[AGENT ERROR] Step 1 failed: %v", err)
		os.Exit(1)
	}

	// Step 2: HubSpot Contact Creation
	_, err = asareAgent.ExecuteStep(
		2,
		"HubSpot Contact Provisioning",
		ledger.Action{Method: "POST", URL: baseURL + "/mock/hubspot/contact", Body: map[string]any{"email": "sheikh@voltstudio.in"}},
		lookupInverse("POST", baseURL+"/mock/hubspot/contact"),
	)
	if err != nil {
		log.Printf("[AGENT ERROR] Step 2 failed: %v", err)
		os.Exit(1)
	}

	fmt.Println("\n--- SYSTEM STATE BEFORE AGENT CRASH ---")
	mockservices.PrintSystemState(baseURL)

	// Simulated chaos crash at step 3
	fmt.Println("\n>>> [CHAOS INJECTION] SIMULATING AGENT PROCESS CRASH / TIMEOUT AT STEP 3!")
	log.Printf("[CRASH] Agent LLM reasoning loop timed out or crashed mid-transaction!")
	log.Printf("[CRASH] Step 1 (Stripe) and Step 2 (HubSpot) were completed, leaving ORPHAN/CORRUPT state!")
	os.Exit(1) // Simulate kill -9: WAL left with COMPLETED steps, no recovery marker
}