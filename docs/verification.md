# Verification record

Recorded: 2026-09-16. Results apply to exact code snapshots, not automatically to later changes. Documentation-only commits do not add runtime behavior.

## Item management and callback runtime

Verified code snapshot: `eba0a7764455b1d688d98ac7e45131aae0d11459`.

[Go checks, run 35057544409](https://github.com/EugenSleptsov/hoarder/actions/runs/35057544409), job `104670755351`, completed successfully. Environment: GitHub-hosted Ubuntu 24.04 / Linux amd64, Go 1.27.1, CGO enabled.

The job passed module download/integrity and unchanged-lock-file checks, formatting, `go vet`, race tests with coverage, named-test listing, executable build/help and the offline simulation.

## Local execution of the same code

Environment: Linux amd64, Go 1.23.2 with `GOTOOLCHAIN=local`, GCC and the pinned SQLite dependency vendored from the checksum-verified source-bundle workflow. This is an offline compatibility test environment, not a recommended production toolchain. Vendored dependencies and local binaries were not committed.

| Check | Result |
|---|---|
| `gofmt -l cmd internal` | No unformatted project files |
| `go vet ./...` | Passed |
| `go test -race -count=1 -cover -timeout=45s ./...` | Passed, all ten packages |
| `go test -list='^(Test\|Fuzz)' ./...` | 77 named test functions, two fuzz targets |
| `go build -o bin/hoarder ./cmd/hoarder` and `-h` | Passed |
| `go run ./cmd/hoarder-sim` | Passed |
| `FuzzParse`, 2 seconds / two workers | Passed, 114,334 executions in this run |
| `FuzzIntervalValidation`, 2 seconds / two workers | Passed, 115,907 executions in this run |

The suite grew from 61 to 77 named tests in this slice. Fuzz counts describe brief smoke tests, not exhaustive validation.

Local statement coverage: `internal/bot` 79.7%, `internal/item` 81.1%, `internal/sqlstore` 66.5%, `internal/dialog` 78.0%, `internal/forecast` 95.2%, `internal/schedule` 90.5%, `internal/app` 71.4%, `internal/telegram` 69.5%, `cmd/hoarder` 35.8%, `cmd/hoarder-sim` 68.6%. These are executed-statement percentages, not forecast accuracy or complete failure-mode coverage.

## Added regression scenarios

- Pause/resume preserving physical evidence, rate, samples and the original possibly overdue plan date; another item's state and date remain identical.
- Archival requiring confirmation, cancellation leaving the item unchanged, restoration onto pause and manual stock checks not implicitly resuming notifications.
- Old observations and configuration confirmations rejected after newer item changes; competing confirmations applying only once; wrong sender rejected.
- Reserve/lead-time/check-gap edits requiring confirmation, replaying after restart and never adding physical stock. Identical settings leave revision and plan unchanged.
- Shopping recommendations being reconciled when reserve policy changes, without recording a purchase or fabricating consumption evidence.
- A pending proactive digest being cancelled after its last item is paused; filtering one item while still delivering the other without moving that other's plan.
- Control/event/plan/outbox rollback on injected transaction failure.
- Migration from schema v1 to v2 preserving legacy event bytes and duplicate receipt hashes, refusing a different owner and replaying mixed legacy/control events.

Existing integration tests continue to exercise inline onboarding, continuation after restart, callback acknowledgments, failed edits, unknown observations, no-use, hidden purchases, daily sessions, pagination, delivery windows, expired buttons and message ownership. SQLite tests use real temporary databases; Telegram tests use the real client against a local fake HTTP server. No live messages are sent.

## Earlier verified snapshots

The previous runtime snapshot `99bf8c0466b6010c5e2d254581b25f7e01698d8c` passed [Go checks, run 35031295529](https://github.com/EugenSleptsov/hoarder/actions/runs/35031295529).

The initial core `311155a92254887eb5c2b354ae4139c56899cfcb` passed local and [GitHub checks, run 35026271718](https://github.com/EugenSleptsov/hoarder/actions/runs/35026271718). It had 29 named tests and one fuzz target then; those counts are historical.

An intermediate management commit failed CI formatting in `internal/bot/service.go`. The verified snapshot above includes the correction and passed the complete job; failed intermediate runs are not treated as successful tests.

## Not performed or guaranteed

No owner token, live Telegram connection, production deployment or household pilot was used. There is no demonstrated forecast-accuracy advantage over simple reminders. Annual seasonality, calibrated stockout probabilities, multiple users/groups, health endpoints, interprocess leases and automatic backup/record retention remain unimplemented.

First-send acceptance before local message-ID persistence can duplicate an external message. A network request already in flight cannot be atomically cancelled by pausing an item. Tests cover idempotent state mutation and selected recovery paths, not exactly-once external delivery, every power-loss boundary or arbitrary concurrent processes. Deploy one process per token/database.
