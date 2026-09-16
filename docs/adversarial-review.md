# Adversarial review — 2026-09-16

Baseline: `c2c5a004ed7d885721f667761f310534d9ab2807`. The initial audit bundle commit `33852f60d5b2a18c2cb6a2e75760c78f592ffb17` changed only the source-bundle workflow, not application behavior.

Fixed and tested code: `5f965cd757f8a9c8b9f43f18b59ea83b9e1ebe08`, tree `61083c7cc0e2ed1cc80b9e9ffb0b3f46e34d8872`.

Six distinct defects were reproduced by tests before the corresponding fixes. Severity below is engineering triage, not a CVSS score: P1 means availability/data-loss risk; P2 means delivery/recovery failure; P3 means an input-validation/UI boundary defect with narrower prerequisites. This is an application review, not an independent penetration-test certification.

## Findings and repairs

### A01 / P1 — A foreign ordinary message stopped the daemon

Location: former `internal/bot/service.go:Handle`, now `internal/bot/updates.go`.

A missing update receipt left `err = sqlstore.ErrNotFound`. Foreign ordinary messages and unsupported update shapes bypassed both handlers, leaving that error intact. The transaction rolled back, the polling cursor did not advance, and `cmd/hoarder` returned an update-processing error. Restart could receive the same unacknowledged input again. The existing foreign-callback tests did not exercise the ordinary-message branch.

Before-fix regression output: `untrusted/unsupported input poisoned update loop: record not found`.

Repair: clear the expected lookup miss before routing; durably record an ignored decision and advance the cursor without replying or changing an item. The regression submits foreign private messages, group messages, unsupported updates and repeats, then checks owner interaction still works and the existing item/plan are identical.

Test: `TestAdversarialForeignMessagesAndUnsupportedUpdatesAreDurablyIgnored`.

### A02 / P1 — A permanently high polling offset could acknowledge unseen lower IDs

Locations: `cmd/hoarder/main.go`, `internal/bot/updates.go`.

The Telegram Update contract says the next identifier is random after at least a week without new updates. The old daemon always reused a monotonic persisted high offset, including after restart. A newer lower identifier could therefore be skipped by the first request. This is a possible case allowed by the API, not a claim that every idle week resets IDs downward.

The fake API exercises the real `run` loop with old ID 1000 and new ID 100. Before repair it requested offset 1001 instead of 0. It also exercises replay of an already committed update, an empty response and a later lower ID.

Repair: start a polling worker at offset 0, relying on durable receipts to avoid repeating mutations; replayed receipts still advance the acknowledgment cursor. After a successful empty response, compare-and-reset the cursor to 0, unless another update has advanced it. No negative offsets, queue deletion or automatic webhook deletion are used.

Tests: `TestAdversarialDaemonPollingResetsAcknowledgedCursor` (two scenarios), `TestPollingResetReacknowledgesReceiptWithoutAnotherMutation`, `TestEmptyPollingResponseCannotResetConcurrentlyAdvancedCursor`.

### A03 / P1 — Valid long text batches exceeded the response-size guard

Location: `internal/telegram/client.go:Updates`.

With no explicit batch limit, a returned batch of 100 messages containing 4096 three-byte Unicode characters each exceeded the client's 1 MiB response cap. The client rejected the whole response, without processing updates or advancing its cursor. Repeating the same request reproduced the error. An input need not pass household authorization to reach the transport decoder.

Before-fix regression output: `legal long messages poison polling: invalid Telegram response`.

Repair: explicitly request at most 20 updates per batch. The reproduction then succeeds with complete, untruncated text. The response cap remains: this fixes the reproduced ordinary-text batch, not every conceivable oversized or deeply nested individual Update object.

Test: `TestAdversarialPollingLargeValidUpdatesDoesNotPoisonBatch`.

### A04 / P2 — A stale delivery enumeration deleted a newer cleanup job

Location: `internal/bot/delivery.go:Flush`.

Flush enumerated jobs once. During the network request for job A, an interleaved callback could retire screen B and replace B's render job with a markup-only cleanup. Flush then used its stale copy of B's old render job, saw the screen was retired, and deleted the newly queued cleanup. The physical old keyboard remained visible. The single-process service permits these interleavings; the test injects one explicitly and does not claim the current sequential daemon always creates this race.

Before-fix test found a non-empty keyboard after both flush attempts.

Repair: treat the initial list as candidates only; reload each current job in the transaction immediately before deciding what to send. Missing and newly deferred jobs are skipped without deleting a replacement. A second regression checks a changed retry date survives and the job is delivered later.

Tests: `TestAdversarialRetirementOfLaterJobSurvivesFlushSnapshot`, `TestAdversarialDeferredCandidateKeepsUpdatedRetryDate`.

### A05 / P2 — A 429 delay did not protect other outgoing jobs after restart

Location: `internal/bot/delivery.go:Flush`.

Only the failed job's retry time was durable. The daemon waited in memory, but another ready job could be sent after restarting within the returned delay. The reproduced test sent one other job during the 120-second cooldown.

Repair: persist the outgoing delivery cooldown under `runtime/flood_until`, consult it before all outbox sends/edits/keyboard removal, and preserve the longest known cooldown. Persist it even when the triggering screen became obsolete during the request. It never changes item plans or forecast evidence. This scope is the outbox, not a blanket distributed rate limiter for every Bot API method.

Tests: `TestAdversarialFloodCooldownCoversOtherJobsAndRestart`, `TestAdversarialFloodCooldownExpiresWithoutChangingItems` (blocked at 119 seconds; delivered at 120).

### A06 / P3 — An unknown callback protocol could trigger cleanup before validation

Location: `internal/bot/service.go:routeCallback`.

The route checked stored screen ownership and expiry before validating the protocol. An authorized sender could supply an unknown prefix with a known expired screen ID and cause that keyboard to be removed instead of receiving a side-effect-free rejection. This required owner identity and a known screen; it was not cross-user inventory access.

Before-fix regression output: `unknown callback protocol erased a known message`.

Repair: validate supported protocol, canonical identifier/generation/index syntax and field count before any screen binding or cleanup operation. Existing stored-action, message, owner and item-revision checks remain.

Test: `TestAdversarialMalformedProtocolCannotRetireKnownScreen`.

## Onboarding and existing guarantees rechecked

Added `FuzzQuickWizardActionSequences`: generate up to 128 offered actions, including back/cancel and time jumps that expire physical observations. Check deterministic transitions, strictly increasing screen generation, rejection of old generations, nonnegative stock ranges, positive initial duration and absence of reachable dead-end screens.

The existing suite still checks explicit creation confirmation, multi-name validation and deduplication, interrupted/resumed forms, per-item and whole-queue cancellation, terminal keyboard-free receipts, same-message replacement, different-message cleanup, failed edits, transactional rollback/replay, legacy migration and item independence. No additional onboarding failure was found in the executed sequence campaign; this is not an exhaustive proof.

## Verification and operation

See [verification.md](verification.md) for exact commands, counts, environment and CI evidence. There are 114 named tests (10 added in this review) and three fuzz targets (one added). Existing tests were not disabled or relaxed. No owner credentials or live Telegram requests were used. The local Git tree hash matches the uploaded tested tree; vendored dependencies and generated databases were not committed.

Database schema remains v3; this review adds no new migration. A v1/v2 installation still follows the existing pre-upgrade backup procedure in [running.md](running.md). Restart with the new executable is necessary to use the repaired polling loop. Code publication is not deployment.

Residual limitations: one process per token/database, no exactly-once first send across the Telegram-accept/local-commit crash window, network/permanent API failures may delay or prevent visible cleanup, finite response-size guard, no live visual rendering or destructive power-loss test, and no measured forecasting advantage or seasonal learning. The review does not certify arbitrary concurrent processes or every possible Telegram payload.

## Primary API reference

Telegram Bot API: [Update](https://core.telegram.org/bots/api#update) and [getUpdates](https://core.telegram.org/bots/api#getupdates). The API defines identifier behavior, offset acknowledgment and batch limits; the defects and repair effectiveness above are established by repository code and controlled tests, not by the documentation alone.
