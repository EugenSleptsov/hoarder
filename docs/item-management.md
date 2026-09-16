# Item lifecycle and explicit settings

Status: implementation in progress. This document is a contract, not evidence that all controls are already wired.

## Invariants

- Lifecycle and configuration changes affect exactly one item. Other items' evidence and selected dates remain unchanged.
- Pause only suppresses proactive questions and shopping reminders. It never asserts zero use, changes the consumption rate or re-anchors physical stock.
- Resume restores the stored plan, including an overdue deadline. It does not restart the forecast from a full package or send an unsolicited message outside the daily window.
- Removal from the registry is reversible archival with explicit confirmation. Inventory and immutable observation history are retained. Restore returns the item to paused status, so reactivation is deliberate.
- Reserve/lead-time/check-gap changes must be explicit, revision-checked commands with an audit trail. Increasing a reserve is never a reaction to prediction error. Policy changes do not add physical stock.
- Old observation, settings and confirmation buttons cannot overwrite newer inventory or lifecycle state. Reject them as stale and acknowledge the callback.
- Selection and daily-delivery rendering exclude paused/archived items even when a previously built menu still contains their IDs. No global question cap or competition is introduced.

## Planned button flow

The registry retains quick stock checks. A separate item-management menu opens a card with current configuration and controls: pause/resume, settings, archive confirmation. An archive view offers restoration. Settings are finite button choices for the fixed reserve, purchase lead time and maximum observation gap. Any proposed change has a confirm/cancel screen.

All screens bind server-side opaque action data to inventory revision and lifecycle version. One accepted action consumes the screen. Durable update/callback receipts remain the first line of duplicate protection. Network sends stay outside transactions.

## Acceptance

Tests must cover pause without stock mutation, overdue resume, archive/cancel/restore, old question and old settings buttons, repeated and competing confirmations, no influence on a second item, pending digest filtering, transactional rollback, database reopen and replay of explicit configuration events. Existing schema-v1 data remains readable; lifecycle defaults to active for an item without a control record.

An archive is not secure erasure. Database backups and Telegram message history may retain names and earlier observations. Permanent deletion and regulatory erasure are outside this control.
