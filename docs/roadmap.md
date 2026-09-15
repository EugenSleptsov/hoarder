# Implementation roadmap

Status: 2026-09-15. The initial foundation is now wired into `cmd/hoarder`: a single-owner Telegram polling bot with SQLite, inline callbacks, independent stored plans and a daily digest. This is an initial runnable vertical slice, not a completed production service or a validated forecasting product. See [running.md](running.md) and [verification.md](verification.md).

## Invariants retained

Items have independent histories, rates, observations and question dates. Shared code, a delivery slot and pagination do not permit pooled learning, competing-item priorities or a daily item cap. The reserve is fixed leeway, never an automatically increasing target stock. Predictions remain primary; reporting every consumption/purchase/reserve opening is not required.

Unknown information is not zero consumption. A purchase recommendation is not stock. An unknown-time purchase triggers a current snapshot, not an invented addition. Historical snapshots are not used to learn a rate through unreported replenishment. External Telegram delivery is not claimed to be exactly once.

## H001 — Durable items: implemented initial slice

`internal/sqlstore` implements the application transaction interface with an owner-bound SQLite database. Initial states and immutable events support replay; projections carry revisions, command receipts detect duplicates and mismatched payloads, and event/projection/receipt changes roll back together. Known additions increase the existing stock without changing reserve policy. Tests cover rollback, duplicate commands, conflicting concurrent revisions, database reopening, immutable events and consistent backup/restore.

Schema v1 has `metadata`, `items`, `events` and `records`. `records(kind,id,body)` stores typed JSON for plans, receipts, screens, sessions, purchase intents, delivery jobs and runtime state. The separate conceptual tables in the architecture design are not all separate physical SQL tables in this version. Item/event referential integrity is enforced in SQL; workflow-record relations are checked by the transactional service. Further normalization, indexes and retention require an explicit migration.

Only the initial schema migration exists; future/unrecognised schemas and owner mismatches fail closed. Replay is version checked. An identical duplicate command is a no-op and returns the current projection, not a separately retained historical response object.

## H002 — Independent stored plans: implemented; lifecycle controls pending

A stock answer saves the changed item's plan in the same transaction. The dispatcher reads stored dates rather than recalculating overdue deadlines on every tick. Unknown answers do not reset the physical observation anchor. Integration tests confirm that changing one item does not change another item's state or plan.

Pending: pause/resume, deleting/deactivating items, editable item configuration, explicit shared schedule migration and a measured per-item retry/backoff policy. Current unanswered/uncertain due items can be offered again the next day. Silence must never be converted into no-use evidence; pausing notifications must not imply no consumption.

## H003 — Daily sessions and outbox: implemented single-process slice

A unique local-date session stores every due item. Empty days are silent; menus page through all due items without deferring lower-priority items. Catch-up is limited by the configured time window and current local date. Expired automatic deliveries are not retried overnight; unresolved item plans remain due for the next window.

The outbox sends after transaction commit. Known message IDs are edited. Network/server errors are retried; Telegram `retry_after` is respected. Permanent `400`/`403` failures are retained for diagnosis. A first-send crash between Telegram acceptance and storing its ID can still create a duplicate message. An authenticated callback can recover that unrecorded message ID without repeating an inventory mutation.

Tests cover repeated ticks, restart, pagination of ten due items, failed delivery across a restart, empty-day silence and missed-window recovery. Calendar edge cases remain covered by the separate schedule tests.

Pending: interprocess worker leases, persisted global flood-limit cooldown, operational inspection/retry of failed jobs, cleanup of old records and a wider crash-injection matrix. Deploy one process per database/token. A local mutex is not a distributed delivery guarantee.

## H004 — Inline callback conversations: implemented for one owner

Onboarding uses text for the name and buttons for reserve policy, duration hint, closed packages and open-package level. Existing-item flows include unknown/no-use, optional purchase-history clarification and a current snapshot after "already bought". Purchase recommendations are stored separately from stock.

Callback IDs are opaque and versioned. The service validates the private chat, sender, accessible message, allowed action, screen generation, active item question and expected item revision. Update/callback receipts and the polling offset are persisted with the resulting state and outbox. Repeated taps cannot apply another observation. The daemon acknowledges rejected callbacks as well as accepted ones.

No-use requires an explicitly stated whole-household interval. Partial closed-package counts and delayed history answers expire after 15 minutes rather than being combined with a much newer stock observation. Physical closed stock is asked independently of the reserve-policy flag.

Pending: multiple authorised household members/group chats, richer package quantities, editing earlier answers, conversation burden metrics and improved minimal-question selection. The current UI accepts up to three closed packages and one open package, and can require several taps. Do not describe it as zero-effort tracking. Polling conflicts fail explicitly; webhook deletion and dropping Telegram updates are not automatic recovery actions.

## H005 — Runnable process: implemented; operations hardening pending

`cmd/hoarder` has validated environment/secret-file configuration, graceful signal cancellation, explicit owner binding, long polling, callback acknowledgement, daily dispatch and `-backup`. Consistent backups use `VACUUM INTO`; restore instructions use a new database path rather than mixing a backup with old WAL files. Credentials and inventories are not logged by default.

CI builds the executable, verifies module integrity, runs vet/race tests and exercises the fake Telegram endpoint against temporary SQLite. Recorded results distinguish local tests, GitHub-hosted checks and live deployment.

Pending: health endpoint, deployment packaging/service unit, automated backup scheduling and retention, safe configuration-edit migrations, separate error diagnostics, cleanup and broader cancellation/crash tests. No owner token, live Telegram connection, deployment or household pilot was used to claim this milestone.

## H006 — Pilot and model evaluation: not implemented

Keep reserve size fixed while comparing the adaptive per-item scheduler against fixed independent reminders. Record the prediction available before an answer, rather than retroactively fitting it. Measure answers and physical inspections per item, unknown/ignored prompts, unnecessary early suggestions, missed replenishment boundaries, reserve erosion and real stockouts.

Simulations should include steady use, coarse observations, hidden purchases, long no-use, intermittent demand, seasonal patterns and abrupt shocks. Demand while stock was available must be distinguished from a period already out of stock. Existing synthetic scenarios verify invariants, not real forecasting quality. Historical observations do not reveal what a user would have said on unasked dates.

## H007 — Richer predictors: not implemented

`interval-baseline-v1` remains a heuristic, non-seasonal range model. More expressive models need versioning, replay tests and measured per-item benefit before adoption:

- Observation likelihoods and interval-censored transitions rather than dropping all censored learning intervals.
- Robust rate changes and temporary shocks without reserve growth.
- Separate occurrence/quantity models for intermittent use where justified.
- Annual seasonality supported by repeated observations of that item, with shrinkage toward no seasonal effect and rolling-origin evaluation.
- Learning that item's purchasing delay when timing is actually observed.
- Question selection based on whether the answer changes an action, without a shared item budget.

No pooled household/category training under the current independence requirement. Do not label heuristic range endpoints as calibrated probabilities.

## Outside the current scope

Automatic orders/payments, medication-critical guarantees, expiry accounting, receipt/email/calendar integrations, global shopping optimisation and an LLM dependency need separate product decisions. They are not prerequisites for the callback bot.
