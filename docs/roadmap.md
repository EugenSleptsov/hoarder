# Implementation roadmap

Status: 2026-10-25. `cmd/hoarder` is a runnable Telegram bot for one household (an owner plus invited family members in their own private chats) with SQLite, inline callbacks, independent saved plans, daily delivery, item management (including renaming, full sets and larger reserves) and an optional MCP endpoint for an external shopping agent. This is an initial implementation, not a production-readiness or forecast-accuracy claim.

## Invariants

Prediction is primary. Each item has its own evidence, rate, plan and recommendation. The fixed physical reserve is leeway, never an automatically growing stock target. Only the common delivery slot and presentation are shared; there is no competing-item question budget. Unknowns, non-use, observations and purchases remain distinct. External Telegram sends are not exactly once.

## H001 — Durable items: implemented initial slice

SQLite persists original item states, immutable events, projections, revisions and command receipts. Transactions protect event/projection/plan/result changes. Tests exercise rollback, duplicates, concurrency, reopen, replay and consistent backup/restore.

The physical tables are `metadata`, `items`, `events` and generic typed-JSON `records`. Plans, screens, sessions, intents, jobs and runtime receipts use `records`; conceptual tables in the original architecture are not all separate SQL tables. Workflow relations are transactionally checked by the service.

Schema v3 adds a reader compatibility fence for lifecycle/configuration events, quick onboarding, adding queues and markup-only delivery. Schema v4 fences full sets and paged counts. Schema v5 fences per-chat screens, message keys and text input of family members. Owner-checked migration preserves historical payloads and receipt hashes; prior binaries must refuse v5. Normalization, indexes, retention and larger migration fixtures remain future work.

## H002 — Independent plans and lifecycle: implemented initial slice

Stored dates survive ticks and restart instead of being perpetually recalculated into the future. Unknowns do not reset physical evidence. Pure planning has no access to another item.

Pause/resume, reversible confirmed archival, restoration onto pause and explicit reserve/lead-time/check-gap settings are now implemented through callbacks. Controls preserve stock/rate/observation anchors. Lifecycle controls keep the saved deadline, including overdue dates; configuration changes replan exactly one item. No-op settings preserve revision and deadline. Recommendations reconcile after explicit configuration changes/resume without creating purchases.

Renaming (2026-10-25) is a name-only `reconfigure` event that keeps evidence, links and the saved, possibly overdue, plan date. The text prompt is bound to the shown revision and expires after 30 minutes. Name shortcuts in `/add` can be hidden and restored.

Full sets (2026-10-25): an explicit per-item number of packages in use after replenishment, on top of the reserve. The reserve (now 0–24 by buttons) still decides when to recommend; the full set sizes the recommendation up to set plus reserve in `/shopping` and as `units_to_full_set` for the agent. It changes neither stock nor the question date. Stock checks count up to 99 closed packages by paged buttons.

Pending: package-unit changes, correcting historical answers, explicit shared schedule migration and a measured per-item retry/backoff policy. Unanswered or uncertain due items may still appear the next day. Notification pause is never evidence of zero consumption.

## H003 — Daily sessions and outbox: implemented single-process slice

Every eligible due item enters the daily session; pages do not defer items because of others. Empty days are silent. Catch-up is limited by the configured window and local date. Failed jobs retain their state; retries honor Telegram's returned delay. Old automatic deliveries do not resume outside the daily window.

Pending proactive menus are checked again before delivery. Paused/archived/no-longer-due items are filtered; an empty digest is cancelled, a changed menu gets a new opaque screen ID. A request already in flight cannot be cancelled atomically. First-send acceptance before local message-ID persistence can still produce duplicates.

The outgoing bot-wide flood-limit cooldown is now persisted and survives restart. Each delivery candidate is reloaded before sending, so stale enumeration cannot delete newer cleanup or retry jobs.

Pending: interprocess leases, failed-job inspection/retry controls, retention and broader crash injection. Deploy one process per token/database.

## Callback cleanup and quick adding — implemented

Terminal stock/onboarding/cancel answers now leave keyboard-free receipts. Intermediate steps replace buttons. Superseded questions queue markup-only cleanup; per-message screen ownership prevents stale taps from erasing current keyboards. Tests cover restart, legacy messages and late HTTP success/failure racing new callbacks.

Quick onboarding offers name shortcuts, reserve, stock presets and explicit confirmation; duration is optional and its default disclosed. Multi-line lists up to 20 names are validated/deduplicated and confirmed per item. `/add` resumes; `/cancel` abandons the remaining queue. Old wizards remain readable. See [callback-onboarding.md](callback-onboarding.md).

## H004 — Callback conversations: implemented for one household

Onboarding uses name shortcuts or text, inline reserve and stock choices, optional duration and explicit confirmation. Existing checks distinguish unknown, confirmed household non-use and purchase-history clarification. Unknown-time purchases cause current snapshots rather than fake additions. Partial checks expire after 15 minutes rather than silently mixing old and new quantities.

Management uses `/manage` and `/archive` plus inline controls. Settings preview old/new values and archive has a confirmation step. Expected item revisions protect both stock observations and management actions. Callback/update receipts and polling offset persist with durable decisions. Tests cover repeated, stale, concurrent and unauthorised callbacks, restart, failed edits and message-scope checks.

Family access (decision 2026-10-25): the owner issues one-time invitation links (24 hours, at most 5 open and 10 members) from `/family`; an invited person uses the same shared items from their own private chat. Screens, Telegram message keys and pending text input are scoped per chat; callbacks are checked against the sender and the screen's chat. Each person turns their own daily digest on or off; the selection and dates of due items stay per item. The newest open question for an item wins in every chat, and a stale list button in another chat re-renders the current list. Only the owner invites, revokes and removes; a removed member loses access and buttons at once, and their keyboards are cleared. Agent messages go to the owner only. Tests cover sharing, per-chat message IDs and input, one-time/expired/revoked invitations, removal, forged callbacks, per-person digests across DST and restart, and agent routing; sixteen new seeded faults have named witnesses.

Pending: group chats, several open packages in one answer, corrections and interaction-burden metrics. Current input supports up to 99 closed packages and one open package and may require several taps; it is not zero-effort tracking.

## Adversarial audit — implemented repairs

The 2026-09-16 review reproduced and fixed six defects: foreign-message poison updates, idle-period polling offsets, oversized ordinary-text batches, stale delivery enumeration, restart-bypassed outgoing flood cooldown and malformed callback cleanup. Tests also cover receipt replay, deferred jobs and randomized onboarding sequences. See [adversarial-review.md](adversarial-review.md) for prerequisites and residual limits; no production-readiness claim follows from these repairs.

## H005 — Runnable process: implemented; operational hardening pending

Validated environment or token-file configuration, owner-bound database, graceful cancellation, polling, dispatch and `-backup` are implemented. Backup uses consistent SQLite snapshots; restore uses a fresh path to avoid mixing old WAL files.

CI checks module integrity and formatting, runs vet/race tests, builds the bot and runs the offline simulation. An optional short-lived source-bundle workflow makes tracked code plus checksum-verified vendored dependencies available for offline development. It contains no live inventory or credentials and is not deployment packaging.

A systemd user unit (`deploy/hoarder.service`) exists, and since 2026-10-24 one owner deployment runs with live Telegram. Family access has not been exercised live. No household-pilot results exist yet.

Pending: health endpoint, container packaging, backup scheduling/retention, error diagnostics and broader failure tests.

## H006 — Pilot and model evaluation: not implemented

Compare adaptive schedules with fixed independent reminders using the same fixed reserve. Record forecasts before answers. Measure responses and physical checks, unknowns/ignored prompts, unnecessary recommendations, missed replenishment boundaries, reserve erosion and stockouts. Sparse historical answers cannot reveal responses on unasked dates.

Add controlled steady-use, hidden-purchase, no-use, intermittent, seasonal and shock scenarios. Synthetic regression tests alone do not establish real-world accuracy.

## H007 — Richer per-item forecasts: not implemented

`interval-baseline-v1` remains non-seasonal with heuristic ranges, not calibrated probabilities. Candidate improvements include censored observations, local rate changes, intermittent occurrence/quantity models, per-item lead-time learning and selective questions. Annual seasonality needs repeated observations and better rolling-origin performance for that item. No pooled household/category learning and no automatic reserve growth.

## H008 — Agent integration over MCP: implemented initial slice

Decision 2026-10-24: an external shopping agent (an MCP client such as a Claude Code session with the Knuspr MCP) may read recommendations and report deliveries; hoarder itself stays LLM-free and never orders. `internal/mcp` is a dependency-free Streamable HTTP server (POST/JSON only, bearer token, loopback-only, browser origins refused). `bot.AgentTools` exposes `shopping_needs`, shop links, `record_delivery`, `request_checks` and `notify_owner`. See [agent-integration.md](agent-integration.md).

Per-item forecasts are evaluated at an agent-chosen moment without cross-item input; `min_units_to_cover` is a derived minimum and `units_to_full_set` a derived cart size up to the explicit full set plus reserve; neither is a stored target. Deliveries are exact additions keyed by order, idempotent, and rejected when newer evidence exists. Agent-initiated questions and messages are an explicit exception to the common slot, capped at 10 messages per local day. Tests cover protocol rules, authentication, idempotency, double-count protection, item independence and configuration; four new seeded faults have named witnesses.

Live on 2026-10-24: the bot runs on a server as a systemd user service with real Telegram; a Claude Code session reaches hoarder through the SSH stdio bridge (`deploy/mcp-stdio.py`) and Knuspr through OAuth, with Knuspr checkout, order and account tools denied by client permissions. Only reads were exercised.

Pending: a first real cart and recorded delivery; delivery correction; several links per item; retention of `agent_notice` records; a wake-up of the sender instead of waiting for the next poll cycle.

## Outside scope

Automatic ordering/payment, medication-critical guarantees, expiry accounting, receipt/email/calendar integration, global shopping optimization and an LLM dependency inside hoarder require separate product decisions. H008 is the decision for an external agent; it does not authorise ordering or payment.
