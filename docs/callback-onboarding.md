# Callback keyboards and quick onboarding

Implemented 2026-09-16. This describes the runtime, not merely a proposed UI. Real Telegram rendering/live credentials have not been tested; local HTTP integration checks assert the actual Bot API request payloads and persisted SQLite state.

## Keyboard lifecycle

| Situation | Result |
|---|---|
| Intermediate wizard answer | Edit the same message with the next step and new generation of buttons |
| Completed stock answer, item creation or cancellation | Edit text to a receipt and explicitly send `inline_keyboard: []` |
| Question superseded in a different message | Retire its callbacks and queue markup-only removal, preserving text |
| Expired known button is pressed | Reject it and queue safe cleanup |
| Old generation within an otherwise current wizard | Reject it and reconcile the CURRENT step; do not erase its buttons |
| Old screen replaced in the same message | Reject it without removing the newer keyboard |
| Foreign chat/user, inaccessible message or unrecognised callback | Reject without authorising arbitrary message editing |

Navigation and settings screens remain interactive: replacing their old actions with the next screen is intentional. The keyboard-free receipt is used at the end of a stock/onboarding conversation. Command hints in that receipt let the user reopen `/items`, `/today` or `/add`.

`answerCallbackQuery` acknowledges the tap separately. It does not remove inline markup. `ClearKeyboard` calls `editMessageReplyMarkup` with an explicit empty array and no text. A not-modified error is successful reconciliation.

The durable `message_screen` record binds a Telegram message to the current server screen. Cleanup checks that binding before sending. Legacy messages without a ledger adopt an unambiguous live screen; ambiguity must not be resolved by guessing which keyboard to erase.

Retirement is transactional and immediately makes the callback invalid, even while Telegram is unavailable. Cleanup is a `delivery.ClearOnly` job using the existing retry/error policy. A retired screen cannot regain mutation authority through a delayed send. Completed duplicate taps may repair the stored receipt but never rerun item creation or stock reduction.

Late HTTP errors are checked against the latest screen/job so an old failed edit cannot delete a newer receipt queued by an already committed callback. In-flight cleanup followed by a newer screen queues reconciliation for that newer screen.

**Limits:** no database transaction can atomically control Telegram. An already in-flight edit/removal can be briefly visible before reconciliation. Network failure can delay keyboard removal, permanent `400`/`403` remains in `failed_delivery`, and first sends can duplicate across the send/commit crash window. The service is still single-process; no cross-process sender lease is claimed. Expired keyboards are cleaned on interaction/invalidation, not by a complete periodic sweep of all historical messages.

## Adding an item

`/add` and the Add button open typing shortcuts for common names. The shortcuts are names only; rates, reserves and inventories are never borrowed from another item or a learned category.

After a name, `NewQuick` starts:

1. Reserve policy: fixed zero or one package.
2. Current stock: common presets or the detailed closed/open-package form.
3. Review and explicit Add confirmation.

The common path takes three button taps after the name. Current-stock choices are available regardless of reserve policy: choosing reserve does not silently select an extra package. A user can go Back, edit stock or optionally provide a usual package duration before saving. With no duration hint, the review discloses a 30-day cold-start assumption and the existing broad rate range. This is not measured consumption.

Current-stock observation time is when the quantity was selected, not when the message was sent or the final confirmation arrived. Confirmation after 15 minutes requires a fresh quantity answer. Detailed closed/open observations have the same freshness check. Old persisted wizards without `Quick` continue through the previous state machine.

## Multiple names and interruption

Names can be sent as separate lines, including `/add Name1\nName2`. Up to 20 unique names, 80 Unicode characters each. Input is validated before starting; blank lines are skipped, case-insensitive duplicates are removed, existing names (including archived items) are not recreated. Names are checked again in the creation transaction to prevent a duplicate from competing/legacy wizards.

A persisted `runtime/adding` queue holds only the onboarding workflow. It does not share forecast parameters or question dates. Every name gets its own independent configuration, quantity and explicit confirmation. Completion queues the next name, leaving the previous receipt without buttons.

`/add` resumes the unfinished form with retained choices and a new opaque screen ID; old buttons are retired. The item ID stays fixed. A new list is not accepted while another adding flow is incomplete. The wizard Cancel skips that one item; `/cancel` abandons the entire remaining queue and preserves already committed items. Cancelling a name prompt removes its keyboard. An invalid name does not partially create a batch.

## Persistence compatibility

Schema v3 is a reader fence for new dialog steps, adding queues, message ownership and markup-only delivery. Opening v1/v2 as the correct owner upgrades the marker transactionally without rewriting item events, projections or receipt hashes. Existing old wizards remain readable. Prior binaries refuse v3 rather than misinterpreting new steps or resurrecting old buttons.

Make a consistent pre-upgrade backup with the previous binary. Do not lower `user_version`; rollback uses the previous binary and a separate pre-upgrade snapshot. See `running.md`.

## Regression checks

`keyboard_test.go`, `keyboard_lifecycle_test.go`, `keyboard_races_test.go`, `onboarding_test.go`, `adding_test.go` and migration tests cover payloads, duplicate/stale/foreign taps, terminal receipts, same-message replacement, different-message cleanup, network failures, in-flight races, restart/resume, batch cancellation, late confirmation, duplicate names and unchanged item independence. Synthetic tests establish those behaviors, not real-world forecast accuracy.

Primary API documentation: https://core.telegram.org/bots/api#editmessagereplymarkup and https://core.telegram.org/bots/api#answercallbackquery.
