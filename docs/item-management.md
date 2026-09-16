# Item lifecycle and explicit settings

Status: implemented initial slice, 2026-09-16. Code and tests live in `internal/item/controls.go`, `internal/bot/management.go` and their regression tests. This does not establish production readiness or forecasting accuracy.

## User flow

`/manage` or the registry's management button opens a list of non-archived items. Each item card exposes a manual stock check, settings, pause/resume and archive confirmation. `/archive` lists archived items and offers restoration onto pause. Normal stock checks retain their previous direct path; management is not an extra mandatory step.

Settings offer fixed reserve 0/1 package, purchase lead time 1/3/7/14 days and maximum observation gap 7/30/90/180 days. Selecting a value displays the old and new configuration; only confirmation changes it. Saving identical configuration is a no-op for the item revision, history and dates.

## State semantics

Pause suppresses proactive questions and hides the item's shopping recommendation. It does not assert no-use, change the rate, update a physical anchor or reset the forecast. Manual observation remains possible without resuming notifications.

Pause, resume, archive and restore preserve the previously selected plan date. Only its expected item revision changes. Resume does not move an overdue deadline into the future or send an unsolicited message outside the daily window. Restore clears the archived flag but leaves the item paused; reactivation is explicit.

Archival requires confirmation and retains immutable history. It is not secure erasure. Names and observations may also remain in backups and Telegram history. An archived name cannot be reused through ordinary onboarding; restore the original item instead.

An explicit configuration change preserves physical evidence, rates and samples. It recalculates only that item's plan and purchase recommendation. Neither enabling a reserve nor increasing lead time adds stock. The algorithm never changes the reserve automatically. Resume also reconciles the recommendation against current projected stock without changing evidence or the saved plan date.

## Events and compatibility

`pause`, `resume`, `archive`, `restore` and `reconfigure` are immutable item events. `Paused`, `Archived` and optional `ControlAt` belong to the same aggregate, using the same revision as observations. `ControlAt` guards chronology but is not a physical observation time. Future stock projection continues from the original physical anchor.

Configuration is an optional event payload omitted for legacy events. Existing receipt hashes retain their original encoding. Replay starts from the original stored state and applies observation and control events in sequence. Events, projection, plan, intent, invalidated active question, callback receipt and response outbox share one transaction.

Schema v2 keeps the existing table layout but fences out older binaries that do not understand lifecycle state. Startup upgrades v1 after checking ownership; historical JSON and events are not rewritten. Missing lifecycle fields in old items mean active. Back up with the old binary before the first new-version open. Do not manually downgrade `user_version`.

## Callback and delivery safety

Management actions are server-side and bound to an opaque screen, allowed action, owner, source message and expected item revision. Old settings, archive confirmations and stock answers cannot overwrite newer state. Each accepted menu action consumes its screen. A lifecycle/configuration change invalidates an unfinished stock dialog for that item.

Previously built page IDs are only candidates; presentation is filtered against each item's current state. Immediately before proactive delivery, an inactive or no-longer-due item is removed. If no items remain, the pending digest is cancelled. If actions change, a new opaque screen is queued: old callback indices are never rebound to different items. Remaining eligible items keep their own plan dates and no global cap is introduced.

SQLite cannot atomically cancel a request already in flight to Telegram. A message can arrive after a concurrent pause, but stale callback checks still prevent an old observation from overriding the control. First-send duplicate-message limitations remain documented in `running.md`.

## Regression coverage

Tests cover pause without evidence changes, overdue resume, cancellation and confirmation of archival, restored pause, manual checks while paused, stale stock and settings screens, competing confirmations, wrong sender, no-op settings, independent second-item state/plan, recommendation reconciliation, pending digest filtering, transaction rollback, database reopen and mixed legacy/control replay. They run on fake Telegram HTTP and real temporary SQLite, not a live chat.

## Still outside this slice

Renaming, package-unit conversion, arbitrary numeric settings, permanent data erasure, correcting old events, shared schedule migration, multi-user control and automatic cleanup remain separate work. The forecasting algorithm and seasonal-learning limitations are unchanged.
