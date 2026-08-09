# HN Submission (copy-paste ready)

## Pre-filled submit link

https://news.ycombinator.com/submitlink?u=https%3A%2F%2Fgithub.com%2Fsheikhasif192006-cloud%2Fasare&t=Show%20HN%3A%20Saga%20transactions%20for%20AI%20agents%20%E2%80%94%20automatic%20rollback%20for%20half-executed%20side%20effects

## Title options (pick one)

1. Show HN: Saga transactions for AI agents — automatic rollback for half-executed side effects
2. Show HN: I gave my AI agent write access. It charged $500, then died. I built rollback.
3. Show HN: ASARE — Write-Ahead Log + LIFO compensation for LLM agents
4. Show HN: Agent state reconciliation — undo for non-deterministic side effects

Recommendation: title 2 is the HN classic "story first" style. Title 1 is safer/descriptive. HN
show rules: URL must be the repo, title starting with "Show HN:", no sales-y language.

## First comment (post it yourself right after submitting)

I'm the author. The core problem: agents fail by *succeeding at the wrong step*. Step 1 and 2
complete (money charged, CRM record created), step 3 dies, and no observability tool can undo
the external side effects. Observability tells you *that* it failed; nothing tells you *how to
undo it*.

I applied the saga pattern (30 years old, used in distributed systems) to LLM agents:

- Write-Ahead Log records every write action BEFORE it's forwarded
- Inverse actions declared in YAML (charge -> refund, create -> delete)
- $response.* templating resolves IDs at rollback time
- LIFO compensation on crash, verified across real process restarts
- Production pattern is a transparent HTTP proxy: agent points at the proxy URL, zero SDK changes

What's honest about the current state: services are mocks proving the mechanism. Real adapters
for Stripe/HubSpot/Okta are the next step. Crash-injection harness in the repo shows the math.

The uncomfortable part: every company letting an agent touch Stripe, Okta, or a production DB
is one mid-workflow crash away from a double charge. The gap isn't agent intelligence, it's
accountability for side effects.

## Where to post

- HN: use the pre-filled link above
- Reddit: r/golang, r/MachineLearning, r/LangChain (adapt title, no Show HN)
- Lobsters: lobste.rs if you have an invite