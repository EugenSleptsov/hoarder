# Verification record — initial reference core

Date: 2026-09-15. Code snapshot: `311155a92254887eb5c2b354ae4139c56899cfcb`. Documentation-only additions after that snapshot do not imply additional runtime functionality.

## Executed locally

Environment: Linux amd64, Go 1.23.2 with `GOTOOLCHAIN=local`. This is a recorded compatibility test environment, not a recommended production toolchain.

| Check | Result |
|---|---|
| `go test -race -count=1 -cover ./...` | Passed, all six packages |
| `go vet ./...` | Passed |
| `gofmt -l .` | No unformatted files |
| `go run ./cmd/hoarder-sim` | Successful deterministic JSON scenario |
| `go test -run='^$' -fuzz=FuzzIntervalValidation -fuzztime=2s -parallel=2 ./internal/item` | Passed; 103,854 executions in this short run |

There are 29 named `Test...` functions and one fuzz target with two seed inputs. The fuzz run is brief smoke testing, not exhaustive validation.

Statement coverage in the local race-test run:

| Package | Coverage |
|---|---:|
| `internal/forecast` | 95.2% |
| `internal/schedule` | 90.5% |
| `internal/item` | 79.8% |
| `internal/telegram` | 77.0% |
| `internal/app` | 71.4% |
| `cmd/hoarder-sim` | 68.6% |

Coverage measures executed statements, not prediction accuracy, production readiness or complete failure-mode coverage.

## Verified on GitHub Actions

[Go checks, run 35026271718](https://github.com/EugenSleptsov/hoarder/actions/runs/35026271718) completed successfully for the code snapshot above. The workflow configures Go 1.27.1 and runs `make check` plus the offline simulation. GitHub reported completion at 2026-09-15 21:34:02 UTC.

## Not performed or not implemented

No live Telegram messages, owner credentials, production deployment or household pilot were used. SQLite persistence, migrations, callback/business command handlers, authorisation integration, shopping-intent lifecycle, daily session/outbox workers and the runnable bot daemon are not implemented yet.

The reference core does not learn annual seasonality or calculate calibrated stockout probabilities. Its synthetic scenario demonstrates deterministic transitions and item independence; it does not establish real-world forecast performance. Crash-safe delivery and the complete end-to-end conversation remain acceptance tasks in `roadmap.md`.
