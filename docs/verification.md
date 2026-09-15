# Verification record

Recorded: 2026-09-15. Results apply to the exact snapshots below, not automatically to later commits. Current branch status is also visible in GitHub Actions.

## Runnable callback runtime

Verified code snapshot: `eeb8f5c0890a512504a4987137297a07d8d6750c`.

[Go checks, run 35030465652](https://github.com/EugenSleptsov/hoarder/actions/runs/35030465652), job `104587519706`, completed successfully. Environment: GitHub-hosted Ubuntu 24.04, Linux amd64, Go 1.27.1, CGO enabled and GCC available.

| Check | Result at this snapshot |
|---|---|
| `gofmt` check | Passed |
| `go vet ./...` | Passed |
| `go test -race -count=1 -cover ./...` | Passed |
| `go run ./cmd/hoarder-sim` | Passed |
| Pinned SQLite module download/checksum verification | Passed |

The runtime integration tests use an injected HTTP transport into the real Telegram client, a local fake Telegram server, temporary real SQLite databases and controlled timestamps. They do not contact Telegram.

Verified scenarios include:

- Adding an item with inline callback buttons, restarting between dialog steps and completing the same persisted dialog.
- Sending `answerCallbackQuery` and editing the existing Telegram message rather than asking for typed percentage commands.
- Duplicate update, same callback in a new update, stale screen generation, wrong sender and expired screen rejection without another inventory mutation.
- Unknown observations continuing the forecast while preserving the physical-observation anchor and another item's state/plan.
- "Already bought" re-anchoring the current snapshot rather than inventing an addition or training through unknown purchase history.
- Explicit no-use confirmation preserving stock without setting future consumption to zero.
- One daily session containing ten independently due items across pages, without duplicate creation on restart.
- Failed proactive delivery not being retried outside the configured daily window, with due items retained for the next day.
- Empty-day silence and recovery of an accepted first-send message ID through an authorised callback.
- Stored schedule changes being rejected rather than silently moving existing item deadlines.

SQLite tests additionally cover event/projection/receipt rollback, exact duplicate and conflicting command IDs, concurrent optimistic revisions, reopen and replay, immutable events, transactional polling offset, schema/owner checks and consistent backup/restore.

Three additional regression tests were added in `0aca387c560f95483aa36a4556af5f68031b944b`: concurrent competing callbacks, accepted observations across failed edits/restart, and callback message-scope/accessibility failures. Their result must be read from a subsequent CI run; it is not implied by the earlier run above.

The workflow was subsequently extended to verify module integrity and unchanged lock files, list named tests, explicitly build `cmd/hoarder` and run the executable's `-h` path. These additional steps likewise require a run containing that workflow revision.

## Local checks in the callback implementation session

Only the dependency-free dialog package was race-tested locally with Go 1.23.2; source files were formatted locally. Full database/bot tests were run on GitHub Actions, not claimed as local container execution. The local workspace did not have the downloaded SQLite dependency or a complete cloned source tree.

## Historical initial core

The original snapshot `311155a92254887eb5c2b354ae4139c56899cfcb` passed local race tests, vet, formatting, the two-item simulation and a short interval-validation fuzz smoke test on Linux amd64 / Go 1.23.2. It contained 29 named test functions and one fuzz target at that time. [Initial Go checks, run 35026271718](https://github.com/EugenSleptsov/hoarder/actions/runs/35026271718) also passed on Go 1.27.1. These historical counts are not the current suite size.

Coverage describes exercised statements, not forecast accuracy or production readiness. Short fuzz runs and selected failure scenarios are not exhaustive proofs.

## Not performed / not guaranteed

No live Telegram token, messages, production deployment or household pilot was used. There is no demonstrated accuracy benefit over simple reminders yet. Annual seasonality, calibrated stockout probabilities, multi-user/group support, health endpoints, worker leases and automatic backup/record retention are not implemented.

The first external `sendMessage` may be duplicated if Telegram accepts it but the process crashes before the returned message ID is saved. Tests cover idempotent state mutation and selected recovery paths, not exactly-once external delivery or every operating-system/power-loss boundary. Runtime deployment is single-process per database/token.
