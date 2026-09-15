# Architecture

Status: target design plus a deliberately small executable reference core. This document does not imply that every component is implemented; see `roadmap.md` for the implementation boundary.

## 1. Shape of the system

Use a modular Go monolith, one process and one household initially. No message broker, microservices, LLM or separate model-serving service is necessary.

```text
Telegram updates / onboarding
             |
             v
Application commands -> transaction -> item event history + projection
                                  \-> question/session state + outbox
                                                |
                     pure per-item predictor <--+
                              |
                      next question deadline
                              |
                 household daily-window adapter
                              |
                 one daily session / outbox -> Telegram
```

The scheduler knows a collection of item IDs but never fits a joint model. Delivery order is a stable presentation detail, not a priority signal that changes item deadlines.

## 2. Package boundaries

- `internal/item`: validated item configuration, observations, interval quantities, immutable-style aggregate transitions. No clock calls, network, database, other items or random global state.
- `internal/forecast`: a versioned deterministic baseline and a `Predictor` interface. Input is exactly one item's state and an explicit timestamp. Output includes stock interval, action, next-check horizon, reasons and limitations.
- `internal/schedule`: IANA-zone calendar-window selection. No consumption inference.
- `internal/telegram`: a small Bot API HTTP adapter. No stock calculations. Token-bearing request URLs must never appear in returned/logged errors.
- `internal/app`: ports and command / transaction contracts for the eventual durable bot runtime.
- `cmd/hoarder-sim`: offline, repeatable demonstration of the reference core. It is not a deployed Telegram daemon.

Production runtime additions: SQL repository, migrations, application command handlers, daily-session worker and executable `cmd/hoarder`. Keep the core usable without those adapters so forecasting can be replayed and tested locally.

## 3. Data representation

Quantities are standard package equivalents. A quantity is an interval `[low, high]`, not a supposedly precise percentage. A rate is a nonnegative interval of units per elapsed 24-hour day. Rate and stock bounds are heuristic plausibility ranges in the baseline, **not calibrated confidence intervals**.

The reserve policy has `enabled` and a fixed `units` value. Default onboarding can use zero or one, but the domain supports explicit fixed sizes. Nothing in an observation updates policy. Package-equivalent conversion is an explicit configuration concern.

The item aggregate stores:

- stable item ID, name and policy;
- last stock anchor and its effective observation timestamp;
- rate estimate, number of usable learning intervals and algorithm version;
- last actual stock observation used as a learning anchor;
- explicitly recorded additions since that anchor;
- latest contact / explicit-unknown timestamp separately from stock observation time;
- monotonically increasing revision;
- durable event identity for duplicate detection in the persistence layer.

Store question state separately. Creating or sending a question does not imply an observation. A reply to an old question reports current state at the time actually checked unless the user explicitly supplies another effective time. Support `received_at` separately from `observed_at` in storage.

## 4. Commands and events

Proposed commands:

- CreateItem(config, current stock, optional duration hint).
- ObserveStock(item, interval, observed_at, replenishment history completeness).
- RecordAddition(item, amount, occurred_at) — optional proactive convenience.
- ReportUnknown(item, at) — no physical stock evidence.
- ConfirmNoUse(item, from, to, no unreported additions) — explicit whole-household interval, not personal non-use.
- ChangePolicy(item, expected revision, explicit user change).
- PauseItem / ResumeItem — explicit, reversible, not inferred from silence.

Persist immutable events. Derived snapshots can be rebuilt with the recorded model version; upgrading the model must not rewrite what the user said. A correction is a new event referencing the corrected event, not a silent edit of history.

ObserveStock updates the current stock anchor even when purchases are unknown. It trains the rate only when the consumption interval is identifiable. Explicit purchases add stock at their actual time. Historical timestamps cannot be handled by simply applying an old event after newer state; replay or reject the out-of-order command and ask for correction.

## 5. Persistence design

Use SQLite for the first durable deployment, with a single writer, foreign keys, transaction boundaries and a schema migration table. Driver choice is an implementation task; do not hide an untested dependency in the pure core.

Logical tables:

- `households`: ID, authorised Telegram chat ID, IANA timezone, local daily time.
- `items`: household ID, immutable ID, configuration JSON, revision, paused flag.
- `item_events`: item ID, command/event ID, kind, effective/received timestamps, original payload; unique(item_id, command_id).
- `item_projections`: item ID, revision, model version and serialized projection.
- `questions`: item ID, due date, expected revision, type, status and latest permissible reply time.
- `daily_sessions`: household ID, local date, created time, status; unique(household_id, local_date).
- `session_questions`: session ID, question ID, deterministic position. Every independently due item is included; pagination is not deferral.
- `outbox`: operation ID, method, payload, attempts, next attempt, status, Telegram message ID when available.
- `processed_updates`: bot instance ID, Telegram update ID, command result reference.

A mutating update runs in one database transaction: authorise -> check event identity and expected revision -> append event -> reduce -> recompute affected item's question -> save projection -> persist result/outbox -> mark update processed -> commit. Retry on an optimistic-concurrency conflict after rereading; do not overwrite another household member's answer.

Advance the polling offset only after a corresponding durable decision. Invalid/unauthorised updates can be durably marked ignored to avoid poison loops. Never log the token or full private household histories by default.

## 6. Daily delivery and crash windows

The dispatcher chooses a local calendar day, not a 24-hour ticker. A normal day has one configured wall-clock slot; the same household/session key prevents duplicate creation after restarts.

DST policy must be explicit: on an absent wall-clock minute use the first available local minute after it on that date; on a repeated minute choose its first occurrence. Tests cover both. The adapter can map an item's desired deadline to the preceding slot so rounding does not silently spend another day of leeway. If that slot has already passed, use the next available slot and expose that the desired deadline cannot be met.

During the allowed catch-up window, a restart may deliver today's existing session. Outside it, keep questions due for the next slot rather than send at 03:00. An overdue item does not become observed. Keep the catch-up window an explicit deployment setting.

Create session + outbox intent transactionally, then send outside the transaction. After success, record message ID. A crash after Telegram accepted `sendMessage` but before local persistence can duplicate a message on retry. Telegram `sendMessage` offers no application idempotency key; document at-least-once delivery in this crash window, not exactly-once delivery. Subsequent `editMessageText` operations can repair a known message idempotently (treat 'not modified' as successful reconciliation).

Within the same session, an answer can advance to another already-due question by editing the message. This is a response to the user's action, not an independent notification at a different hour. A user may explicitly reopen the session outside the hour; automatic proactive notifications still use the common slot.

## 7. Callback protocol and access control

Use opaque, short server-side question IDs plus a version and selected action. Bind the stored question to household, item, session, expected item revision and allowed answer set. Callback data is not authority to mutate an arbitrary item. Verify chat ownership and, for shared chats, permitted users if configured.

Repeated taps: return the previous result without adding another purchase/observation. Old buttons: acknowledge and explain that the question was superseded; never overwrite newer state. A callback answer is required to end Telegram's client-side progress indication; an answer is not itself a committed stock update.

Use a configured allowlist, not 'the first user who sends /start becomes owner'. Token from an environment variable or secret file, never committed configuration. An exported sample file contains no chat-specific stock observations.

## 8. Cold start, seasons and shocks

The baseline uses the item's own duration hint and coarse snapshots, with wide plausible rates and a maximum check gap. It does not claim to learn seasonality. Later implementations can add per-item seasonal or intermittent-demand state behind the same interface, but must be evaluated on that item's held-out history. Shared fixed algorithm defaults are allowed; pooled household learning is not.

A sudden mismatch re-anchors observed stock and flags changed/unknown behaviour. Do not manufacture a stock addition to make the old prediction fit. Do not automatically grow a reserve. No predictor can guarantee availability if unknown demand consumes all stock between permitted observations.

## 9. Operational scope

The reference module can remain compatible with the locally available Go compiler for reproducible offline tests. Deploy network-facing code using a currently supported Go release with security patches. The release history consulted on 2026-09-15 lists Go 1.27.1; pin a reviewed supported toolchain in CI/deployment rather than interpreting a minimum `go` directive as a security recommendation.

Tests must be deterministic and must never send messages to a real chat. Use a fake clock, HTTP test server and repository transaction contract tests. A live end-to-end run requires the owner's bot token and chosen deployment; absence of that run must be stated in the handoff.

## Primary technical references

- Go time package (calendar arithmetic, location and DST): https://pkg.go.dev/time
- Go transactions: https://go.dev/doc/database/execute-transactions
- Go release/support policy: https://go.dev/doc/devel/release
- Telegram Bot API, getUpdates, CallbackQuery and sendMessage: https://core.telegram.org/bots/api

Product policies above are design decisions. References describe external API/runtime behaviour, not evidence that this household predictor is statistically accurate.
