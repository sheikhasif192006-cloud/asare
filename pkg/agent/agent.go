package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"asare_poc/pkg/ledger"
)

// ASAREAgent wraps an agent's tool executions with WAL logging.
// Every step is logged as PENDING before the network call, then COMPLETED
// with the response data (which compensations can reference via $response.*).
type ASAREAgent struct {
	wal    *ledger.WAL
	execID string
	client *http.Client
}

func NewASAREAgent(wal *ledger.WAL, execID string) *ASAREAgent {
	return &ASAREAgent{
		wal:    wal,
		execID: execID,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (a *ASAREAgent) ExecuteStep(stepIdx int, name string, forward, inverse ledger.Action) (map[string]any, error) {
	// Step A: Log PENDING to WAL before network call
	step := a.wal.LogPending(a.execID, stepIdx, name, forward, inverse)

	log.Printf("\n[AGENT EXECUTE] Step %d: %s | Forward: %s %s", stepIdx, name, forward.Method, forward.URL)

	// Step B: Perform Network Call
	bodyBytes, err := json.Marshal(forward.Body)
	if err != nil {
		a.wal.MarkFailed(step.StepID)
		return nil, err
	}
	req, err := http.NewRequest(forward.Method, forward.URL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		a.wal.MarkFailed(step.StepID)
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		a.wal.MarkFailed(step.StepID)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		a.wal.MarkFailed(step.StepID)
		return nil, fmt.Errorf("step %d (%s) returned status %d", stepIdx, name, resp.StatusCode)
	}

	var respData map[string]any
	json.NewDecoder(resp.Body).Decode(&respData)

	// Step C: Log COMPLETED in WAL with response data
	a.wal.MarkCompleted(step.StepID, respData)
	log.Printf("[AGENT SUCCESS] Step %d completed. Response: %v", stepIdx, respData)

	return respData, nil
}