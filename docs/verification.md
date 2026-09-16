# Verification record

Recorded: 2026-09-16. Verified code snapshot: `4a2988e4da45a458e8eaea391071fcc8c7a7b8b4`, tree `07fcb2630ef214165aa4bd49493a29b3b4a61f46`. Documentation-only changes after this snapshot do not add runtime behavior.

## Callback cleanup and convenient adding

[Go checks, run 35061079358](https://github.com/EugenSleptsov/hoarder/actions/runs/35061079358), job `104681334943`, completed successfully for that code snapshot. The job passed dependency verification, unchanged lock files, formatting, vet/race tests, named-test listing, runnable-bot build/help and offline simulation. CI configures Ubuntu 24.04 / Linux amd64, Go 1.27.1 and CGO.

Local execution: Linux amd64, Go 1.23.2 with `GOTOOLCHAIN=local`, GCC/CGO and offline vendored dependencies from the checksum-verified source bundle. The tested Git tree was compared byte-for-byte by its Git tree hash with the uploaded code tree. Vendor copies, binaries and test databases were not committed. This is a compatibility-test environment, not a production toolchain recommendation.

| Local check | Result |
|---|---|
| `gofmt -l cmd internal` | No unformatted project files |
| `go vet ./...` | Passed |
| `go test -race -count=1 -cover -timeout=45s ./...` | Passed, all ten packages |
| `go test -list='^(Test\|Fuzz)' ./...` | 104 named tests and two fuzz targets |
| `go build -o bin/hoarder ./cmd/hoarder`; `-h` | Passed |
| `go run ./cmd/hoarder-sim` | Passed |
| `FuzzParse`, two seconds / two workers | Passed, 38,876 executions in this run |
| `FuzzIntervalValidation`, two seconds / two workers | Passed, 60,175 executions in this run |

The suite grew from 77 to 104 named tests in this slice. Fuzz runs are brief smoke tests, not exhaustive proofs. Coverage includes bot 79.8%, dialog 72.8%, Telegram adapter 72.2%, SQLite 66.5%; those are executed statements, not forecast accuracy or complete failure-mode coverage.

## New regression scenarios

- Explicit empty `inline_keyboard` and markup-only removal with no text field; not-modified reconciliation and API errors.
- Terminal/cancel receipts without buttons; intermediate steps keeping current buttons.
- Superseded messages cleaned without erasing replacements in the same or another message.
- Expired cleanup surviving network failure and database reopening.
- Failed in-flight edits not deleting delivery of a newer committed receipt; in-flight cleanup followed by current-screen reconciliation.
- Legacy screens without a message ledger not erasing newer keyboards; foreign callbacks cannot trigger cleanup.
- A completed repeated tap repairing receipt text without creating another item or observation.
- Three-tap common onboarding after name selection, explicit confirmation, optional duration, review/back, default-prior disclosure, and reserve policy independent of physical stock.
- Multiline validation/deduplication, confirmation-time name conflict, cancellation of one/all remaining items and resume after restart.
- Late confirmation requiring a fresh physical answer; old persisted onboarding remaining readable.
- v1/v2 migration to v3 preserving event/receipt/dialog bytes; another item's state and plan remaining unchanged.

Three new adversarial regressions initially failed: late failed edit deleting a newer receipt, stale legacy keyboard cleanup erasing a replacement, and duplicate completed taps losing receipt text. The verified snapshot includes their fixes; initial failures are not reported as successes.

## Earlier verified snapshots

The previous runtime snapshot `99bf8c0466b6010c5e2d254581b25f7e01698d8c` passed [Go checks, run 35031295529](https://github.com/EugenSleptsov/hoarder/actions/runs/35031295529).

The initial core `311155a92254887eb5c2b354ae4139c56899cfcb` passed local and [GitHub checks, run 35026271718](https://github.com/EugenSleptsov/hoarder/actions/runs/35026271718). It had 29 named tests and one fuzz target then; those counts are historical.

An intermediate management commit failed CI formatting in `internal/bot/service.go`. The verified snapshot above includes the correction and passed the complete job; failed intermediate runs are not treated as successful tests.

## Not performed or guaranteed

No owner token, live Telegram connection, production deployment or household pilot was used. HTTP tests assert real request shapes against a local fake server, not Telegram's live visual rendering. There is no demonstrated forecast-accuracy advantage over simple reminders. Annual seasonality, calibrated probabilities, multiple users/groups, interprocess leases and automatic backup/record retention remain unimplemented.

Keyboard removal can be delayed by a network failure or prevented by permanent Telegram errors. An already in-flight operation may appear briefly before reconciliation. First sends can duplicate across the send/commit crash window. Tests cover selected races and durable/idempotent application state, not exactly-once external delivery or arbitrary concurrent processes. Deploy one process per token/database.
