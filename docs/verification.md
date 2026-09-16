# Verification record

Recorded: 2026-09-16. Adversarial review baseline: `c2c5a004ed7d885721f667761f310534d9ab2807`. Verified repaired code: `5f965cd757f8a9c8b9f43f18b59ea83b9e1ebe08`, tree `61083c7cc0e2ed1cc80b9e9ffb0b3f46e34d8872`. Later documentation-only commits do not add runtime behavior.

## Adversarial findings

[adversarial-review.md](adversarial-review.md) records six reproduced failures, their prerequisites, regression tests, repairs and residual limitations. The failures were observed before the relevant fixes; the final verification below was run with all repairs applied. No existing tests were disabled or relaxed.

## GitHub Actions

[Go checks, run 35063683158](https://github.com/EugenSleptsov/hoarder/actions/runs/35063683158), job `104689225826`, completed successfully for the verified code above. Steps: module integrity and unchanged lock files, formatting, `go vet`, race tests with coverage, test listing, executable build/help and offline simulation. Workflow environment: Ubuntu 24.04 / Linux amd64, Go 1.27.1, CGO enabled.

The separate source-bundle workflow publishes tracked code with verified vendored dependencies for offline tests. It is not a deployment and contains no live bot credentials or household inventory.

## Local verification

Linux amd64, Go 1.23.2 with `GOTOOLCHAIN=local GOPROXY=off`, GCC 14.2.0 and CGO, dependencies from the checksum-verified source bundle. This is an offline compatibility environment, not the recommended network-facing toolchain. The local and uploaded code trees have the exact same Git tree hash. Vendor, binaries and temporary databases are not committed.

| Command / check | Result |
|---|---|
| `gofmt -l cmd internal` | No unformatted project files |
| `go vet ./...` | Passed |
| `go test -race -count=1 -cover -timeout=45s ./...` | Passed, all ten packages |
| `go test -list='^(Test\|Fuzz)' ./...` | 114 named tests and three fuzz targets |
| `go test -race -run='Adversarial\|Keyboard\|InFlight\|Inflight' -count=20 -timeout=45s ./internal/bot ./internal/telegram ./cmd/hoarder` | Passed |
| `go build ... ./cmd/hoarder` and executable `-h` | Passed |
| `go run ./cmd/hoarder-sim` | Passed |

The suite grew from 104 to 114 named test functions, with an additional wizard-sequence fuzz target. Test functions can contain several scenarios; fuzz execution counts are not additional unit-test counts.

Short, two-worker fuzz campaigns (not exhaustive proofs):

| Target | Requested duration | Executions in this run | Result |
|---|---:|---:|---|
| `FuzzQuickWizardActionSequences` | 4 seconds | 43,652 | Passed |
| `FuzzParse` | 3 seconds | 126,052 | Passed |
| `FuzzIntervalValidation` | 3 seconds | 140,731 | Passed |

Local statement coverage: daemon 68.4%, offline simulation 68.6%, app 71.4%, bot 79.8%, dialog 73.3%, forecast 95.2%, item 81.1%, schedule 90.5%, SQLite 66.5%, Telegram 72.2%. These figures describe executed statements, not security completeness, operational readiness or forecasting accuracy.

## Added checks

Foreign ordinary/group messages and unsupported updates are ignored durably without changing inventory or blocking owner interaction. Polling tests exercise the real daemon loop through a fake HTTP transport: startup after a quiet period, replay of a committed update, an empty response, then a newer lower identifier. Receipt replay must acknowledge without duplicating an item or observation; an old empty response cannot erase a concurrently advanced cursor.

Transport regressions cover valid long Unicode text batches, malformed protocol cleanup, a callback replacing another pending job during delivery, deferred retry preservation, persistent outgoing 429 cooldown after restart, and resumption exactly at the deadline without changing item evidence/plans. The added fuzz target traverses offered onboarding actions, backwards navigation, cancellation and stale physical observations, checking determinism and generation/quantity invariants.

Existing tests still cover terminal keyboard removal, same-message replacements, stale/foreign callbacks, repeated and concurrent answers, failed edit reconciliation, batch adding/cancellation/resume, unknown/no-use semantics, fixed reserves, item independence, SQLite rollback/replay/migration and consistent backup/restore.

## Earlier verified snapshots

Keyboard/onboarding snapshot `4a2988e4da45a458e8eaea391071fcc8c7a7b8b4` passed [run 35061079358](https://github.com/EugenSleptsov/hoarder/actions/runs/35061079358), with 104 named tests and two fuzz targets then. Its earlier green suite did not cover the newly reproduced defects.

Runtime snapshot `99bf8c0466b6010c5e2d254581b25f7e01698d8c` passed [run 35031295529](https://github.com/EugenSleptsov/hoarder/actions/runs/35031295529). Initial core `311155a92254887eb5c2b354ae4139c56899cfcb` passed [run 35026271718](https://github.com/EugenSleptsov/hoarder/actions/runs/35026271718).

## Limits

Tests use real temporary SQLite and fake HTTP endpoints/transports, never owner credentials or live Telegram messages. There was no deployment, household pilot, live visual validation or destructive power-loss testing. Test-controlled interleavings demonstrate specific service-level races, not arbitrary multi-process safety. Deploy one process per token/database.

First-send duplication across the external-send/local-commit window remains possible. Network or permanent API failures can delay/prevent visible keyboard cleanup. The response-size guard remains; the large-batch regression covers ordinary text, not every nested Telegram object. Outgoing flood cooldown is not a distributed limiter for every Bot API method. Forecasts remain heuristic and non-seasonal, without demonstrated real-world superiority to reminders.
