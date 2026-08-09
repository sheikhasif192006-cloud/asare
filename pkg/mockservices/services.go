package mockservices

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type MockDBState struct {
	StripeCharges   map[string]map[string]any `json:"stripe_charges"`
	HubSpotContacts map[string]map[string]any `json:"hubspot_contacts"`
}

type MockDB struct {
	mu    sync.RWMutex
	path  string
	State MockDBState
}

func NewMockDB(path string) *MockDB {
	db := &MockDB{
		path: path,
		State: MockDBState{
			StripeCharges:   make(map[string]map[string]any),
			HubSpotContacts: make(map[string]map[string]any),
		},
	}
	db.load()
	return db
}

func (db *MockDB) load() {
	data, err := os.ReadFile(db.path)
	if err != nil {
		return // no existing state, start fresh
	}
	_ = json.Unmarshal(data, &db.State)
}

// failIfInjected checks the X-Fail-Step header: when present, the mock
// returns 500 to simulate an upstream outage mid-workflow.
func failIfInjected(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Fail-Step") != "" {
		log.Printf("[MOCK] FAILURE INJECTED: %s %s -> 500", r.Method, r.URL.Path)
		http.Error(w, `{"error":"simulated upstream outage"}`, http.StatusInternalServerError)
		return true
	}
	return false
}

func (db *MockDB) saveLocked() {
	data, err := json.MarshalIndent(db.State, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(db.path, data, 0644)
}

// SetupMockServer returns a mock HTTP server whose state persists to disk,
// so that a crash in one process leaves observable side effects for the next.
func SetupMockServer(db *MockDB) *http.Server {
	mux := http.NewServeMux()

	// Stripe Endpoints
	mux.HandleFunc("/mock/stripe/charge", func(w http.ResponseWriter, r *http.Request) {
		if failIfInjected(w, r) {
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		chargeID := id("ch")

		db.mu.Lock()
		db.State.StripeCharges[chargeID] = map[string]any{
			"amount": req["amount"],
			"status": "succeeded",
		}
		db.saveLocked()
		db.mu.Unlock()

		log.Printf("[MOCK STRIPE] Created charge %s for amount %v", chargeID, req["amount"])
		json.NewEncoder(w).Encode(map[string]any{"charge_id": chargeID, "status": "succeeded"})
	})

	mux.HandleFunc("/mock/stripe/refund", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		chargeID, _ := req["charge_id"].(string)

		db.mu.Lock()
		if charge, exists := db.State.StripeCharges[chargeID]; exists {
			charge["status"] = "refunded"
			db.saveLocked()
			log.Printf("[MOCK STRIPE] Refunded charge %s", chargeID)
		} else {
			log.Printf("[MOCK STRIPE] Charge %s not found for refund!", chargeID)
		}
		db.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"status": "refunded", "charge_id": chargeID})
	})

	// HubSpot Endpoints
	mux.HandleFunc("/mock/hubspot/contact", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if failIfInjected(w, r) {
				return
			}
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			contactID := id("cnt")

			db.mu.Lock()
			db.State.HubSpotContacts[contactID] = map[string]any{
				"email":  req["email"],
				"status": "active",
			}
			db.saveLocked()
			db.mu.Unlock()

			log.Printf("[MOCK HUBSPOT] Created contact %s for email %v", contactID, req["email"])
			json.NewEncoder(w).Encode(map[string]any{"contact_id": contactID, "status": "active"})

		case http.MethodDelete:
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			contactID, _ := req["contact_id"].(string)

			db.mu.Lock()
			delete(db.State.HubSpotContacts, contactID)
			db.saveLocked()
			db.mu.Unlock()

			log.Printf("[MOCK HUBSPOT] Deleted contact %s", contactID)
			json.NewEncoder(w).Encode(map[string]any{"status": "deleted", "contact_id": contactID})
		}
	})

	// State Inspector
	mux.HandleFunc("/mock/state", func(w http.ResponseWriter, r *http.Request) {
		db.mu.RLock()
		defer db.mu.RUnlock()
		json.NewEncoder(w).Encode(db.State)
	})

	server := &http.Server{
		Addr:    "127.0.0.1:8888",
		Handler: mux,
	}
	return server
}

func PrintSystemState(baseURL string) {
	resp, err := http.Get(baseURL + "/mock/state")
	if err != nil {
		log.Printf("[MOCK] Failed to fetch state: %v", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var state MockDBState
	json.Unmarshal(body, &state)

	prettyJSON, _ := json.MarshalIndent(state, "", "  ")
	fmt.Printf("Mock Databases State:\n%s\n", string(prettyJSON))
}

func id(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano()%10000)
}