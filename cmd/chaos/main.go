package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"asare_poc/pkg/agent"
	"asare_poc/pkg/compensator"
	"asare_poc/pkg/ledger"
	"asare_poc/pkg/mockservices"
	"asare_poc/pkg/registry"
)

// chaos run <workflow-config.yaml>
// chaos recover
// chaos report --exec <id>
func main() {
	log.SetFlags(log.Ltime)

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "recover":
		recoverCmd()
	case "report":
		reportCmd(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`ASARE Chaos CLI

Usage:
  asare-chaos run [--workflow workflows/onboarding.yaml]
      Execute a workflow with random crash injection, print failure report.
  asare-chaos recover
      Detect unfinished executions in the WAL and roll them back.
  asare-chaos report --exec <execution-id>
      Show the step-by-step WAL history for an execution.`)
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	workflowPath := fs.String("workflow", "workflows/onboarding.yaml", "path to workflow config")
	crashAt := fs.Int("crash-at", 0, "force crash after this step (0 = random)")
	fs.Parse(args)

	baseDir, _ := os.Getwd()
	walFile := filepath.Join(baseDir, "asare_wal.json")
	mockDBFile := filepath.Join(baseDir, "mock_db.json")

	mockDB := mockservices.NewMockDB(mockDBFile)
	server := mockservices.SetupMockServer(mockDB)
	go server.ListenAndServe()
	time.Sleep(200 * time.Millisecond)
	defer server.Close()

	wal, err := ledger.NewWAL(walFile)
	if err != nil {
		log.Fatalf("WAL init failed: %v", err)
	}
	reg, err := registry.Load(filepath.Join(baseDir, "inverse_registry.yaml"))
	if err != nil {
		log.Fatalf("Registry load failed: %v", err)
	}

	baseURL := "http://127.0.0.1:8888"
	lookupInverse := func(method, url string) ledger.Action {
		inv, ok := reg.Lookup(method, url)
		if !ok {
			log.Fatalf("No inverse rule for %s %s", method, url)
		}
		if len(inv.URL) > 0 && inv.URL[0] == '/' {
			inv.URL = baseURL + inv.URL
		}
		return inv
	}

	execID := fmt.Sprintf("exec_chaos_%d", time.Now().UnixNano()/1000000)
	asareAgent := agent.NewASAREAgent(wal, execID)
	log.Printf("[CHAOS] Starting workflow '%s' (exec %s)", *workflowPath, execID)

	steps := []struct {
		name    string
		forward ledger.Action
	}{
		{
			"Stripe $500 Payment Charge",
			ledger.Action{Method: "POST", URL: baseURL + "/mock/stripe/charge", Body: map[string]any{"amount": 500}},
		},
		{
			"HubSpot Contact Provisioning",
			ledger.Action{Method: "POST", URL: baseURL + "/mock/hubspot/contact", Body: map[string]any{"email": "client@example.com"}},
		},
	}

	crashPoint := *crashAt
	if crashPoint == 0 {
		crashPoint = 3 // default: crash after all steps complete (mid-workflow, before a 4th step)
	}

	for i, step := range steps {
		if *crashAt > 0 && i+1 == *crashAt {
			log.Printf("[CHAOS] INJECTING CRASH before step %d (%s)", i+1, step.name)
			break
		}
		_, err := asareAgent.ExecuteStep(i+1, step.name, step.forward, lookupInverse(step.forward.Method, step.forward.URL))
		if err != nil {
			log.Printf("[CHAOS] Step %d failed naturally: %v", i+1, err)
			break
		}
	}

	// Simulate the crash: process dies, WAL left with completed steps.
	if *crashAt > 0 && *crashAt <= len(steps) {
		log.Printf("[CHAOS] CRASH simulated before step %d. Orphan state left in WAL + services.", *crashAt)
		os.Exit(1)
	}
	_ = crashPoint

	fmt.Println("\n--- [CHAOS] System state after run (before recovery) ---")
	mockservices.PrintSystemState(baseURL)
}

func recoverCmd() {
	baseDir, _ := os.Getwd()
	walFile := filepath.Join(baseDir, "asare_wal.json")
	mockDBFile := filepath.Join(baseDir, "mock_db.json")

	mockDB := mockservices.NewMockDB(mockDBFile)
	server := mockservices.SetupMockServer(mockDB)
	go server.ListenAndServe()
	time.Sleep(200 * time.Millisecond)
	defer server.Close()

	wal, err := ledger.NewWAL(walFile)
	if err != nil {
		log.Fatalf("WAL init failed: %v", err)
	}
	unfinished := wal.FindUnfinishedExecutions()
	if len(unfinished) == 0 {
		log.Println("[ASARE] No unfinished executions found. Nothing to recover.")
		return
	}

	comp := compensator.NewCompensator(wal, "http://127.0.0.1:8888")
	for _, execID := range unfinished {
		_, err := comp.RollbackExecution(execID)
		if err != nil {
			log.Printf("[ASARE] Rollback failed for %s: %v", execID, err)
			continue
		}
		log.Printf("[ASARE] Rollback completed for %s", execID)
	}
	fmt.Println("\n--- [CHAOS] Final system state after recovery ---")
	mockservices.PrintSystemState("http://127.0.0.1:8888")
}

func reportCmd(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	execID := fs.String("exec", "", "execution id to report")
	fs.Parse(args)

	if *execID == "" {
		log.Fatalf("--exec required")
	}
	baseDir, _ := os.Getwd()
	wal, err := ledger.NewWAL(filepath.Join(baseDir, "asare_wal.json"))
	if err != nil {
		log.Fatalf("WAL init failed: %v", err)
	}
	steps := wal.StepsForExecution(*execID)
	if len(steps) == 0 {
		fmt.Printf("No steps found for execution %s\n", *execID)
		return
	}
	fmt.Printf("Execution %s\n", *execID)
	for _, s := range steps {
		fmt.Printf("  [%s] step %d: %s (%s)\n", s.Status, s.StepIndex, s.Name, s.Forward.URL)
	}
}