package compensator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"asare_poc/pkg/ledger"
)

// Compensator executes inverse (undo) actions for completed steps of a failed execution.
type Compensator struct {
	wal     *ledger.WAL
	baseURL string
	client  *http.Client
}

func NewCompensator(wal *ledger.WAL, baseURL string) *Compensator {
	return &Compensator{
		wal:     wal,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// RollbackExecution walks the WAL backwards (LIFO) and executes the inverse
// action of every COMPLETED step for the given execution ID.
func (c *Compensator) RollbackExecution(execID string) ([]string, error) {
	var rollbackLogs []string

	log.Printf("\n=======================================================")
	log.Printf("[ASARE ENGINE] TRIGGERING ROLLBACK FOR EXECUTION: %s", execID)
	log.Printf("=======================================================")

	completedSteps := c.wal.GetCompletedStepsForExecution(execID)
	if len(completedSteps) == 0 {
		log.Printf("[ASARE ENGINE] No COMPLETED steps found for %s. Nothing to roll back.", execID)
		return rollbackLogs, nil
	}

	// Iterate in REVERSE order (LIFO)
	for i := len(completedSteps) - 1; i >= 0; i-- {
		step := completedSteps[i]
		inv := step.Inverse

		resolvedBody := resolveBody(inv.Body, step.ResponseData)

		bodyBytes, err := json.Marshal(resolvedBody)
		if err != nil {
			log.Printf("[ASARATOR ERROR] Failed to marshal inverse body for step %s: %v", step.Name, err)
			continue
		}
		req, err := http.NewRequest(inv.Method, inv.URL, bytes.NewBuffer(bodyBytes))
		if err != nil {
			log.Printf("[ASARATOR ERROR] Failed to create inverse request for step %s: %v", step.Name, err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		log.Printf("[ASARATOR ROLLBACK STEP %d] Executing Inverse Action for '%s': %s %s with body %s",
			step.StepIndex, step.Name, inv.Method, inv.URL, string(bodyBytes))

		resp, err := c.client.Do(req)
		if err != nil {
			log.Printf("[ASARATOR ERROR] Inverse call failed for step %s: %v", step.Name, err)
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		msg := fmt.Sprintf("Step %d (%s) rolled back -> Status: %d, Resp: %s",
			step.StepIndex, step.Name, resp.StatusCode, string(respBody))
		log.Printf("[ASARATOR SUCCESS] %s", msg)
		rollbackLogs = append(rollbackLogs, msg)

		// Mark the step as ROLLED_BACK in the ledger
		c.wal.MarkRolledBack(step.StepID)
	}

	return rollbackLogs, nil
}

// resolveBody substitutes template variables like "$response.charge_id" with actual forward response values.
func resolveBody(invBody map[string]any, respData map[string]any) map[string]any {
	resolved := make(map[string]any)
	for k, v := range invBody {
		if strVal, ok := v.(string); ok && len(strVal) > 10 && strVal[:10] == "$response." {
			fieldKey := strVal[10:]
			if actualVal, exists := respData[fieldKey]; exists {
				resolved[k] = actualVal
			} else {
				resolved[k] = v
			}
		} else {
			resolved[k] = v
		}
	}
	return resolved
}