package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type Action struct {
	Method string         `json:"method"`
	URL    string         `json:"url"`	
	Body   map[string]any `json:"body"`
}

type StepLog struct {
	StepID       string         `json:"step_id"`
	ExecutionID  string         `json:"execution_id"`
	StepIndex    int            `json:"step_index"`
	Name         string         `json:"name"`
	Forward      Action         `json:"forward"`
	Inverse      Action         `json:"inverse"`
	Status       string         `json:"status"` // PENDING, COMPLETED, FAILED, ROLLED_BACK
	ResponseData map[string]any `json:"response_data,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

type WAL struct {
	mu    sync.Mutex
	path  string
	Steps []StepLog `json:"steps"`
}

func NewWAL(path string) (*WAL, error) {
	wal := &WAL{path: path, Steps: []StepLog{}}
	// Try to load existing WAL state
	if data, err := os.ReadFile(path); err == nil {
		if len(data) > 0 {
			err := json.Unmarshal(data, &wal.Steps)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshal WAL data: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read WAL file: %w", err)
	}
	return wal, nil
}

func (w *WAL) save() error {
	data, err := json.MarshalIndent(w.Steps, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(w.path, data, 0644)
}

func (w *WAL) LogPending(execID string, stepIdx int, name string, forward, inverse Action) *StepLog {
	w.mu.Lock()
	defer w.mu.Unlock()

	step := StepLog{
		StepID:      fmt.Sprintf("step_%s_%d", execID, stepIdx),
		ExecutionID: execID,
		StepIndex:   stepIdx,
		Name:        name,
		Forward:     forward,
		Inverse:     inverse,
		Status:      "PENDING",
		CreatedAt:   time.Now(),
	}
	w.Steps = append(w.Steps, step)
	w.save()
	return &w.Steps[len(w.Steps)-1]
}

func (w *WAL) MarkCompleted(stepID string, respData map[string]any) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := range w.Steps {
		if w.Steps[i].StepID == stepID {
			w.Steps[i].Status = "COMPLETED"
			w.Steps[i].ResponseData = respData
			break
		}
	}
	w.save()
}

func (w *WAL) MarkFailed(stepID string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := range w.Steps {
		if w.Steps[i].StepID == stepID {
			w.Steps[i].Status = "FAILED"
			break
		}
	}
	w.save()
}

func (w *WAL) MarkRolledBack(stepID string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := range w.Steps {
		if w.Steps[i].StepID == stepID {
			w.Steps[i].Status = "ROLLED_BACK"
			break
		}
	}
	w.save()
}

// FindUnfinishedExecutions returns a list of unique execution IDs that have steps
// in PENDING, COMPLETED, or FAILED status (i.e., not fully ROLLED_BACK or fully COMPLETED without issues)
func (w *WAL) FindUnfinishedExecutions() []string {
	w.mu.Lock()
	defer w.mu.Unlock()

	unfinished := make(map[string]bool)
	executionStates := make(map[string]map[string]bool) // execID -> {status: true}

	for _, step := range w.Steps {
		if _, ok := executionStates[step.ExecutionID]; !ok {
			executionStates[step.ExecutionID] = make(map[string]bool)
		}
		executionStates[step.ExecutionID][step.Status] = true
	}

	for execID, statuses := range executionStates {
		// If any step is PENDING, COMPLETED (and not ROLLED_BACK), or FAILED, it's unfinished
		if statuses["PENDING"] || statuses["FAILED"] || (statuses["COMPLETED"] && !statuses["ROLLED_BACK"]) {
			unfinished[execID] = true
		}
	}

	var result []string
	for execID := range unfinished {
		result = append(result, execID)
	}
	return result
}

func (w *WAL) GetCompletedStepsForExecution(execID string) []StepLog {
	w.mu.Lock()
	defer w.mu.Unlock()

	var completedSteps []StepLog
	for _, step := range w.Steps {
		if step.ExecutionID == execID && step.Status == "COMPLETED" {
			completedSteps = append(completedSteps, step)
		}
	}
	return completedSteps
}

// StepsForExecution returns all steps for an execution in log order (for reporting).
func (w *WAL) StepsForExecution(execID string) []StepLog {
	w.mu.Lock()
	defer w.mu.Unlock()

	var steps []StepLog
	for _, step := range w.Steps {
		if step.ExecutionID == execID {
			steps = append(steps, step)
		}
	}
	return steps
}
