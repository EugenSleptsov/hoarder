# Implementation roadmap

Status: 2026-09-16. `cmd/hoarder` is a runnable single-owner Telegram bot with SQLite, inline callbacks, independent saved plans, daily delivery and item management. This is an initial implementation, not a production-readiness or forecast-accuracy claim.

## Invariants

Prediction is primary. Each item has its own evidence, rate, plan and recommendation. The fixed physical reserve is leeway, never an automatically growing stock target. Only the common delivery slot and presentation are shared; there is no competing-item question budget. Unknowns, non-use, observations and purchases remain distinct. External Telegram sends are not exactly once.

## H001 — Durable items: implemented initial slice

SQLite persists original item states, immutable events, projections, revisions and command receipts. Transactions protect event/projection/plan/result changes. Tests exercise rollback, duplicates, concurrency, reopen, replay and consistent backup/restore.

The physical tables are `metadata`, `items`, `events` and generic typed-JSON `records`. Plans, screens, sessions, intents, jobs and runtime receipts use `records`; conceptual tables in the original architecture are not all separate SQL tables. Workflow relations are transactionally checked by the service.

Schema v2 adds a reader compatibility fence for lifecycle/configuration events. Owner-checked v1 migration preserves historical payloads and receipt hashes; prior binaries must refuse v2. Normalization, indexes, retention and larger migration fixtures remain future work.

## H002 — Independent plans and lifecycle: implemented initial slice

Stored dates survive ticks and restart instead of being perpetually recalculated into the future. Unknowns do not reset physical evidence. Pure planning has no access to another item.

Pause/resume, reversible confirmed archival, restoration onto pause and explicit reserve/lead-time/check-gap settings are now implemented through callbacks. Controls preserve stock/rate/observation anchors. Lifecycle controls keep the saved deadline, including overdue dates; configuration changes replan exactly one item. No-op settings preserve revision and deadline. Recommendations reconcile after explicit configuration changes/resume without creating purchases.

Pending: renaming and package-unit changes, correcting historical answers, explicit shared schedule migration and a measured per-item retry/backoff policy. Unanswered or uncertain due items may still appear the next day. Notification pause is never evidence of zero consumption.

## H003 — Daily sessions and outbox: implemented single-process slice

Every eligible due item enters the daily session; pages do not defer items because of others. Empty days are silent. Catch-up is limited by the configured window and local date. Failed jobs retain their state; retries honor Telegram's returned delay. Old automatic deliveries do not resume outside the daily window.

Pending proactive menus are checked again before delivery. Paused/archived/no-longer-due items are filtered; an empty digest is cancelled, a changed menu gets a new opaque screen ID. A request already in flight cannot be cancelled atomically. First-send acceptance before local message-ID persistence can still produce duplicates.

Pending: interprocess leases, durable bot-wide flood-limit cooldown, failed-job inspection/retry controls, retention and broader crash injection. Deploy one process per token/database.

## H004 — Callback conversations: implemented for one owner

Onboarding uses text for the name and buttons for reserve, duration hint, closed packages and open-package level. Existing checks distinguish unknown, confirmed household non-use and purchase-history clarification. Unknown-time purchases cause current snapshots rather than fake additions. Partial checks expire after 15 minutes rather than silently mixing old and new quantities.

Management uses `/manage` and `/archive` plus inline controls. Settings preview old/new values and archive has a confirmation step. Expected item revisions protect both stock observations and management actions. Callback/update receipts and polling offset persist with durable decisions. Tests cover repeated, stale, concurrent and unauthorised callbacks, restart, failed edits and message-scope checks.

Pending: multiple household members/group chats, richer package quantities, corrections and interaction-burden metrics. Current input supports up to three closed packages and one open package and may require several taps; it is not zero-effort tracking.

## H005 — Runnable process: implemented; operational hardening pending

Validated environment or token-file configuration, owner-bound database, graceful cancellation, polling, dispatch and `-backup` are implemented. Backup uses consistent SQLite snapshots; restore uses a fresh path to avoid mixing old WAL files.

CI checks module integrity and formatting, runs vet/race tests, builds the bot and runs the offline simulation. An optional short-lived source-bundle workflow makes tracked code plus checksum-verified vendored dependencies available for offline development. It contains no live inventory or credentials and is not deployment packaging.

Pending: health endpoint, service/container packaging, backup scheduling/retention, error diagnostics and broader failure tests. No owner token, live Telegram connection, production deployment or household pilot has been used.

## H006 — Pilot and model evaluation: not implemented

Compare adaptive schedules with fixed independent reminders using the same fixed reserve. Record forecasts before answers. Measure responses and physical checks, unknowns/ignored prompts, unnecessary recommendations, missed replenishment boundaries, reserve erosion and stockouts. Sparse historical answers cannot reveal responses on unasked dates.

Add controlled steady-use, hidden-purchase, no-use, intermittent, seasonal and shock scenarios. Synthetic regression tests alone do not establish real-world accuracy.

## H007 — Richer per-item forecasts: not implemented

`interval-baseline-v1` remains non-seasonal with heuristic ranges, not calibrated probabilities. Candidate improvements include censored observations, local rate changes, intermittent occurrence/quantity models, per-item lead-time learning and selective questions. Annual seasonality needs repeated observations and better rolling-origin performance for that item. No pooled household/category learning and no automatic reserve growth.

## Outside scope

Automatic ordering/payment, medication-critical guarantees, expiry accounting, receipt/email/calendar integration, global shopping optimization and an LLM dependency require separate product decisions.
