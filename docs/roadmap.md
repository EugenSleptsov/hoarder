# Implementation roadmap

Status: 2026-09-15. This is a delivery plan, not a claim that unfinished components exist. The owner requested design and frequent direct commits; the first delivery includes executable reference code so the invariants can already be tested.

## Completed foundation

- [x] Product requirements and explicit non-goals.
- [x] Independent item aggregate with validated quantities and fixed reserve policy.
- [x] Copy-on-write event reduction, event identity checks, replay-safe duplicate handling and explicit rejection of out-of-order changes.
- [x] Conservative per-item baseline, absolute deadlines and physical-observation gap cap.
- [x] Calendar slot mapping with explicit DST and missed-deadline semantics.
- [x] Telegram HTTP adapter tested with a local server, including token-safe error handling.
- [x] Independent application planning and interfaces for future persistence.
- [x] Reproducible two-item scenario and GitHub Actions checks.
- [x] Conversation, durability, security and seasonal-extension design.

These pieces are not wired into a running Telegram service. The state reducer's in-memory receipts are useful for replay tests; they are not a substitute for database uniqueness and transactions. The SQLite driver and executable daemon have not been added.

## H001 — Durable item vertical slice

Deliver SQLite migrations and a repository implementing the application transaction contract. Pin and review a driver when this stage is implemented. Persist configuration, immutable events, projection version, item revision and command receipts. Include explicit household ownership in every external access path.

Acceptance:

- A stock answer committed before a crash survives restart and reconstructs the same projection by replay.
- Failure between event append and projection save rolls back both.
- Duplicate command ID with the same payload returns the previous result; a different payload is rejected.
- Concurrent answers use expected revisions; a stale answer cannot overwrite newer state.
- Old effective timestamps are rejected or processed through explicit replay, not applied after newer stock changes.
- Replenishment adds quantity once and never raises the configured reserve.
- Snapshot export, backup and restore are tested using temporary databases.

Do not add Telegram messaging to the transaction. Store an outbox intent instead. Do not mark this milestone complete with only a mock repository.

## H002 — Persisted independent question plans

Store a question plan when an item's state or explicit configuration changes. Persist the selected delivery date and expected revision. A dispatcher reads due persisted plans; it must not continually re-run `BeforeDeadline(now, ...)` on overdue rows and thereby move every missed question to tomorrow.

Acceptance:

- A late scheduler tick still sees the stored question due for today's window.
- Unanswered questions never create stock observations or reset physical-observation time.
- An explicit unknown answer does not erase an overdue evidence/safety deadline.
- Per-item retry/backoff decisions remain separate from the physical forecast and expose overdue risk.
- Adding, removing, reordering or mutating other items leaves this item's model and desired/scheduled dates unchanged.
- Shared-timezone/time changes are explicit calendar configuration changes, not inferred cross-item behaviour.
- No daily question cap or competing-item priority is introduced.

Define pause/resume and inactive-item semantics explicitly. Pausing notifications must not implicitly establish zero consumption while paused.

## H003 — Daily sessions and recoverable delivery

Implement a worker with a fake-clock test harness. One session key per household and local date; all independently due questions enter that session. Use a transactional outbox and stable question order. A first implementation can edit one message to page through the due items.

Acceptance:

- Repeated ticks and restarts cannot create two session records for the same local day.
- An empty due set produces no automatic message.
- Every due item is included; pagination never becomes deferral to another day.
- Timezone tests cover both Berlin DST transitions, an absent/repeated wall-clock minute and a missed startup window.
- Catch-up is limited by explicit local-time policy; the worker does not send unexpected overnight notifications.
- Delivery failures retain the outbox operation; rate limits honor retry delay.
- A crash after Telegram accepts a first send but before storing its ID is documented as a possible duplicate-message window. Do not assert exactly-once external delivery.
- After the message ID is known, retries reconcile its content without duplicating stock events.

Question generation, answer processing and delivery history are distinct. Sending a message is not evidence about a household item.

## H004 — Authorised Telegram conversations

Wire polling, commands and callback handling to the durable service. Store or derive the next polling offset only after updates have a durable outcome. Configure the authorised chat explicitly before accepting mutations.

Implement the flow in `telegram-ux.md`: onboarding, current stock, reserve presence, optional history clarification, shopping intent and confirmation. Keep IDs opaque and short. Bind callbacks to the stored question, household, item, allowed actions and expected revision.

Acceptance:

- A different chat cannot inspect or mutate inventory.
- Double taps, duplicate updates, stale buttons and two household members answering concurrently cannot duplicate an addition or overwrite newer evidence.
- Old notification time is not substituted for the time the current stock was actually checked.
- `Already bought` with unknown timing re-anchors current stock rather than creating a full package at reply time.
- A recommendation creates one open purchase intent, not stock. Follow-up confirms that intent rather than recreating it daily.
- Reserve-present/absent alone is not treated as a precise total quantity; partial observations stay broad or trigger a necessary follow-up.
- No-use wording covers the whole household and an explicit interval.
- Polling/webhook conflict and poison updates fail or advance safely without an infinite processing loop.

The initial UI may need more than one tap for some items. Record interaction burden instead of claiming frictionless tracking prematurely.

## H005 — Runnable service and operations

Add `cmd/hoarder`, validated environment/secret-file configuration, graceful shutdown, migrations on a controlled startup path, health information and backup/restore instructions. Package the executable without embedding secrets or household data. Support one household first.

Acceptance:

- From an empty temporary database, an integration test on a fake Telegram endpoint can add an item, create a daily session, answer, confirm a purchase, restart and obtain the same state.
- Shutdown during an HTTP timeout or database write does not lose an acknowledged domain mutation.
- Logs contain useful operation IDs but neither bot-token URLs nor raw private inventories by default.
- Startup fails clearly for missing authorisation, invalid zone/time or incompatible schema.
- Persisted data survives replacing the process/container.
- The README distinguishes tested local integration from a live deployment.

Only after this stage can the repository describe itself as a runnable bot. A live smoke test requires owner-controlled credentials and a chosen deployment; test credentials must not be requested or committed merely to complete offline development.

## H006 — Pilot and model evaluation

Keep the physical reserve fixed. Compare the independent adaptive scheduler with a fixed per-item reminder baseline. Log the prediction available before each answer, not a retrospectively refitted forecast.

Measure per item: answers, physical inspections, unknown/ignored prompts, early unnecessary suggestions, missed replenishment boundaries, reserve erosion caused by lateness and actual stockouts. Distinguish demand while stock was available from a period already out of stock.

Simulations cover steady demand, coarse answers, hidden purchases, long non-use, variable use, seasonal patterns and abrupt shocks. A scenario exposing a failure is valuable; it is not evidence that the baseline supports that demand class. Historical answers cannot reveal what the user would have answered on unasked dates.

## H007 — More expressive per-item predictors

Only after a measured need, add behind `forecast.Predictor`:

- Explicit observation likelihoods and interval-censored transitions rather than dropping all censored intervals.
- Robust local rate changes and temporary shocks, without automatic reserve growth.
- Separate occurrence and amount models for intermittent use where justified.
- Annual seasonality with shrinkage toward no seasonal effect, requiring repeated sufficiently observed cycles and better rolling-origin evaluation for that item.
- Per-item lead-time learning when the relevant events/times are observable.
- A question-selection policy that compares the usefulness of asking now with the user's interaction cost, without a shared item budget.

All model changes require versioning and replay tests. No pooled household/category learning under the current independence requirement. Do not expose heuristic range endpoints as calibrated percentage probabilities.

## Explicitly outside these milestones

Automatic ordering or payment, medication-critical stock guarantees, perishable expiry accounting, receipt scraping, email/calendar integration, global shopping optimisation and an LLM requirement. Adding any of these would need a separate product decision rather than silently expanding the scope.
