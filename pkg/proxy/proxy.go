package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"asare_poc/pkg/compensator"
	"asare_poc/pkg/ledger"
	"asare_poc/pkg/registry"
)

// Proxy is a transparent HTTP reverse proxy that sits between an AI agent
// and its upstream APIs. Every write request (POST/PUT/PATCH/DELETE) is
// logged to the WAL BEFORE it is forwarded; if the upstream call fails,
// the proxy triggers a LIFO rollback of the current execution.
//
// Agents do not need SDK changes: point them at the proxy URL instead of
// the real API base URL. This is the production sidecar pattern.
type Proxy struct {
	upstream   string
	wal        *ledger.WAL
	reg        *registry.Registry
	client     *http.Client
	mu         sync.Mutex
	activeExec string
}

// New creates a proxy. activeExec is the execution ID used for all steps
// passing through this proxy instance (one proxy = one agent execution).
func New(upstream string, wal *ledger.WAL, reg *registry.Registry, activeExec string) *Proxy {
	return &Proxy{
		upstream:   strings.TrimSuffix(upstream, "/"),
		wal:        wal,
		reg:        reg,
		activeExec: activeExec,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

// Handler returns the http.Handler that intercepts agent API traffic.
func (p *Proxy) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isWriteMethod(r.Method) {
			// Read-only calls pass through untouched.
			p.reverseProxy().ServeHTTP(w, r)
			return
		}

		// Capture request body for WAL logging.
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		var bodyMap map[string]any
		_ = json.Unmarshal(bodyBytes, &bodyMap)
		if bodyMap == nil {
			bodyMap = map[string]any{}
		}

		stepIdx := p.nextStepIndex()
		step := p.wal.LogPending(p.activeExec, stepIdx, r.Method+" "+r.URL.Path,
			ledger.Action{Method: r.Method, URL: p.upstream + r.URL.Path, Body: bodyMap},
			p.lookupInverse(r.Method, r.URL.Path),
		)

		// Forward to upstream.
		upstreamResp, err := p.client.Do(cloneRequest(r, p.upstream, bodyBytes))
		if err != nil {
			p.wal.MarkFailed(step.StepID)
			log.Printf("[PROXY] Upstream call %s %s failed: %v — triggering rollback", r.Method, r.URL.Path, err)
			p.rollbackExecution()
			http.Error(w, "upstream failure: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer upstreamResp.Body.Close()

		respBytes, _ := io.ReadAll(upstreamResp.Body)
		var respMap map[string]any
		_ = json.Unmarshal(respBytes, &respMap)
		if respMap == nil {
			respMap = map[string]any{}
		}

		if upstreamResp.StatusCode >= 400 {
			p.wal.MarkFailed(step.StepID)
			log.Printf("[PROXY] Upstream returned %d for %s %s — triggering rollback", upstreamResp.StatusCode, r.Method, r.URL.Path)
			p.rollbackExecution()
			copyHeaders(w, upstreamResp.Header)
			w.WriteHeader(upstreamResp.StatusCode)
			w.Write(respBytes)
			return
		}

		p.wal.MarkCompleted(step.StepID, respMap)
		log.Printf("[PROXY] %s %s → %d, step %d COMPLETED", r.Method, r.URL.Path, upstreamResp.StatusCode, stepIdx)

		copyHeaders(w, upstreamResp.Header)
		w.WriteHeader(upstreamResp.StatusCode)
		w.Write(respBytes)
	})
}

// rollbackExecution rolls back all COMPLETED steps of the active execution.
func (p *Proxy) rollbackExecution() {
	comp := compensator.NewCompensator(p.wal, p.upstream)
	_, err := comp.RollbackExecution(p.activeExec)
	if err != nil {
		log.Printf("[PROXY] Rollback error: %v", err)
	}
}

func (p *Proxy) nextStepIndex() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.wal.StepsForExecution(p.activeExec)) + 1
}

func (p *Proxy) lookupInverse(method, path string) ledger.Action {
	inv, ok := p.reg.Lookup(method, path)
	if !ok {
		// No compensation rule → empty inverse (logged, not reversible).
		return ledger.Action{}
	}
	if len(inv.URL) > 0 && inv.URL[0] == '/' {
		inv.URL = p.upstream + inv.URL
	}
	return inv
}

func (p *Proxy) reverseProxy() http.Handler {
	target, _ := url.Parse(p.upstream)
	return httputil.NewSingleHostReverseProxy(target)
}

func isWriteMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func cloneRequest(r *http.Request, upstream string, body []byte) *http.Request {
	req, _ := http.NewRequest(r.Method, upstream+r.URL.Path, bytes.NewReader(body))
	req.Header = r.Header.Clone()
	return req
}

func copyHeaders(w http.ResponseWriter, headers http.Header) {
	for k, vv := range headers {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
}
