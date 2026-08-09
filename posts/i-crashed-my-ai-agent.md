# I Crashed My AI Agent 20 Times a Day. Here's What It Did to My "Production" Data.

*Post-mortem writeup for the ASARE project. Original research, real output from the crash-injection harness.*

---

Last month I gave an AI agent write access to my stack. One prompt: "onboard a new client."

The agent had to:

1. Charge $500 in Stripe
2. Create the contact in HubSpot
3. Confirm in Slack

Step 1 and 2 succeeded. The agent died on step 3. A timeout, a hallucination, a killed process, doesn't matter why. What matters is what was left behind:

- A **succeeded** charge of $500. Client's money gone.
- An **active** contact in HubSpot. No one knows who they are.
- A Slack channel that never saw the message.

No error was thrown. The agent didn't "fail" in the way monitoring expects. It just stopped, and the business database was quietly in a state that no code path was designed to produce.

## The part that surprised me

`try/catch` does nothing here. The side effects happened in *external systems* — Stripe, HubSpot — that know nothing about my agent's transaction. A retry makes it worse: now there are two charges and two contacts.

LangSmith, Datadog, Sentry: they tell me *that* my agent stopped. Nothing tells me *how to undo it*.

I checked the "agent reliability" landscape. Everyone is selling observability, evals, and guardrails. Nobody is selling **transaction integrity for non-deterministic code**. So I built it.

## What I built: a saga ledger for agents

The core idea is stolen from distributed systems: the **saga pattern**, adapted for agents that talk to third-party APIs over which you have zero transactional control.

When an agent makes a write call, a middleware layer **logs the action to a Write-Ahead Log before forwarding it**:

```
Agent action (POST /stripe/charge)
        │
        ▼
┌──────────────────────────────────┐
│  WAL: log PENDING               │
│  registry: charge → inverse     │  "undo" declared up front:
│                                  │  charge_id → refund (LIFO)
└──────────────────────────────────┘
        │
        ▼
   external API call (succeeds)
        │
        ▼
   AGENT CRASHES (any reason)
        │
        ▼
┌──────────────────────────────────┐
│  Compensator: walk WAL backwards │
│  POST /stripe/charge     →       │  refund
│  POST /hubspot/contact   →       │  delete contact
└──────────────────────────────────┘
```

The inverse actions are declared **declaratively** in YAML — no code changes to add a new integration:

```yaml
rules:
  - action:
      method: POST
      path: /stripe/charge
    inverse:
      method: POST
      path: /stripe/refund
      body:
        charge_id: "$response.charge_id"   # resolved at rollback time
```

Key design decisions:

- **LIFO rollback.** If step 1, 2 succeed and step 3 dies, undo 2 first, then 1. Order matters in the real world (delete the CRM record before refunding, not after).
- **`$response.*` templating.** The inverse body is written before the call happens, but values like `charge_id` don't exist yet. They're resolved from the forward response at rollback time.
- **Persistent WAL across processes.** The ledger lives in a file. If the process is killed with `-9`, the next boot reads the WAL, finds the unfinished execution, and rolls it back. Verified: crash in one process, clean recovery in the next.
- **Zero SDK change in the agent.** The production pattern is a transparent HTTP proxy. Point your agent at the proxy URL instead of the real API base URL. Every write is intercepted and logged before forwarding. If the upstream dies mid-workflow, the proxy compensates automatically.

## What the crash-injection harness proved

I run a chaos suite that force-crashes the workflow at every step:

```
Step 1 charge    → COMPLETED
Step 2 contact   → COMPLETED
Step 3 (killed)  → FAILED

Recovery (new process):
  [ROLLED_BACK] step 2: DELETE /hubspot/contact → 200
  [ROLLED_BACK] step 1: POST /stripe/refund    → 200

Final state: zero orphan records. Charge refunded. Contact gone.
```

The invariants hold across process restarts. 14/14 assertions in the verification harness.

## The uncomfortable truth

Everyone in 2026 is building agents that can *do* more. Almost nobody is building the layer that makes agents *safe to give write access to*. Every company that lets an agent touch Stripe, Okta, or its production database is one mid-workflow crash away from a customer support ticket, a double charge, or a corrupted sync.

The gap is not intelligence. It's **accountability for side effects**.

## Where this goes

This is an open-source project in its early days, and I'm being straight about what's done and what isn't:

**Done**

- Durable WAL with `PENDING → COMPLETED → ROLLED_BACK` lifecycle
- LIFO compensation engine
- Declarative inverse-action registry (YAML)
- Crash-injection chaos CLI
- Transparent HTTP proxy sidecar (zero agent SDK changes)
- Verified crash-recovery across real process restarts

**Not done yet**

- Real adapters for Stripe, HubSpot, Okta, GitHub (currently mock services proving the mechanism)
- Idempotency keys for non-idempotent retries
- Approval gates for irreversible actions (wire transfers, public posts)
- A hosted control plane with audit history

If your agent writes to anything real, you already have this problem. You just haven't crashed at 2 AM yet.

**Repo: [github.com/sheikhasif192006-cloud/asare](https://github.com/sheikhasif192006-cloud/asare)**

---

*Comments, corrections, and "you're overcomplicating this" all welcome. The saga pattern has 30 years of literature behind it for humans; applying it to LLM agents is the new part.*