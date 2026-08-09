# ASARE — Agent State & Action Reconciliation Engine

**Saga transactions & automatic rollback for AI agents.**

When an AI agent executes a multi-step workflow (charge a card in Stripe, create a contact in HubSpot, send a Slack message) and crashes mid-way, it leaves orphan side-effects: money charged, records created, state corrupted across systems.

ASARE wraps every agent action with a **Write-Ahead Log (WAL)** and a **compensating-action registry**. On crash or timeout, it walks the log backwards (LIFO) and executes the inverse of every completed step — automatically. Your business state ends up either 100% done or 0% touched. No manual cleanup. No corrupted databases.

## Why this exists

LangSmith, Datadog, and Sentry tell you *that* your agent failed. They don't undo the damage. Traditional `try/catch` can't help either — the side-effects happened in external systems (Stripe, HubSpot, Okta) that know nothing about your transaction.

ASARE is the transaction layer agents never had.

## How it works

```
Agent action (POST /stripe/charge)
        │
        ▼
┌─────────────────────────────┐
│  ASARE WAL (write-ahead)    │  step logged PENDING → COMPLETED
│  inverse_registry.yaml      │  forward action → compensating action
└─────────────────────────────┘
        │
        ▼
    external API call
        │
        ▼
   CRASH / TIMEOUT
        │
        ▼
┌─────────────────────────────┐
│  ASARE Compensator          │  walks WAL backwards (LIFO),
│                             │  executes inverses:
│   POST /stripe/charge       │    POST /stripe/refund
│   POST /hubspot/contact     │    DELETE /hubspot/contact
└─────────────────────────────┘
```

## Quick start

```bash
go build -o asare.exe ./cmd/asare

# Phase 1: run the demo workflow; it crashes at step 3
# (leaves orphan Stripe charge + HubSpot contact, both persisted)
./asare.exe

# Phase 2: recovery mode — detects the unfinished execution,
# rolls back every completed step, prints clean final state
./asare.exe -recover
```

## The inverse-action registry

Compensation rules are declarative — no code changes to add a new integration:

```yaml
rules:
  - action:
      method: POST
      path: /mock/stripe/charge
    inverse:
      method: POST
      path: /mock/stripe/refund
      body:
        charge_id: "$response.charge_id"   # resolved from forward response
```

`$response.<field>` placeholders are substituted with values from the original forward call's response.

## Repository layout

```
cmd/asare/            demo CLI (crash / recover modes)
pkg/ledger/           write-ahead log (PENDING → COMPLETED → ROLLED_BACK)
pkg/compensator/      LIFO rollback execution
pkg/agent/            agent wrapper that logs steps to the WAL
pkg/registry/         YAML inverse-action registry
pkg/mockservices/     persistent mock Stripe/HubSpot for demo & tests
inverse_registry.yaml sample compensation rules
```

## Roadmap

- [x] WAL ledger with durable persistence
- [x] LIFO compensation across process restarts (verified crash-recovery)
- [x] Declarative inverse-action registry (YAML)
- [ ] Real adapters: Stripe, HubSpot, Okta, GitHub
- [ ] Agent Chaos CLI (automated crash-injection tester)
- [ ] Hosted control plane: transaction history, audit logs, approval gates

## License

Apache 2.0
