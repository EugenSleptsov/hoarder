# Durable callback runtime — implementation checkpoint

Requested continuation: the bot must use real Telegram inline keyboard callbacks, not ask users to type percentage commands. This document is a contract; checked implementation status belongs in the README and roadmap.

## Planned vertical slice

1. SQLite persistence for item projections, immutable events, selected question plans, conversations, callback receipts, outbox operations and polling offset.
2. Persisted button-driven onboarding and stock snapshots. Text is needed only for the item's name; reserve policy, duration hints and rough stock answers use inline callbacks.
3. Opaque callback tokens scoped to the configured chat, conversation revision and allowed action. Old buttons and double taps must not apply another event.
4. A runnable one-household polling process with a daily dispatcher. The configured common local-time slot is the only automatic notification window; responses to explicit user actions may be sent immediately.
5. Offline integration tests using temporary SQLite databases and a fake Telegram API, including restart and failed-send recovery.

## Callback semantics

- Answer every callback query, including rejected, stale and repeated taps. Acknowledgement itself is not proof that inventory has changed.
- Commit the observation, next plan, callback receipt, polling offset and delivery intent in one transaction. Send Telegram messages only after commit.
- Callback data must fit Telegram's 1–64 byte constraint. Never accept item IDs, quantities or ownership from unvalidated callback payloads.
- `25/50/75/100%` describes the currently open standard package, not all closed spares. Ask about closed units separately when needed.
- `Не знаю` is not `Не использовали`. No-use needs an explicitly stated whole-household interval.
- `Уже купили` without an exact time is a current-snapshot flow, not an invented addition at reply time.
- A recommendation and an actual replenishment remain distinct states. Reserve policy stays fixed.
- Newer item evidence invalidates old question revisions. An old screen must never overwrite a newer answer.
- Unknown purchase history must not train the consumption rate. A short follow-up can explicitly confirm no unrecorded purchases when useful.

## Failure boundaries

Duplicate external `sendMessage` delivery remains possible after Telegram accepts a request but before its result is committed locally. Durable application state and callback application must still be idempotent. An outbox must not retry old proactive notifications outside the configured delivery window.

Persist already chosen question dates; a daily tick never replans every item. Repeated scheduler ticks create at most one session record per local date. Queueing/paging must not defer another independently due item to another day.

## Tests required before claiming a runnable bot

- Button payloads, allowed actions and every conversation transition.
- Cross-chat and unauthorised-user rejection; missing/inaccessible message safety.
- Duplicate update, repeated callback with a new update ID, stale token and concurrent replies.
- Snapshot/addition distinction and unchanged fixed reserve.
- Transaction rollback, database reopening, replay and schema compatibility.
- Common-hour dispatch, no-question silence, overdue stored dates and catch-up boundaries.
- Fake Telegram end-to-end onboarding → daily question → callback answer → restart.

Primary API reference: https://core.telegram.org/bots/api#callbackquery and https://core.telegram.org/bots/api#inlinekeyboardbutton.
