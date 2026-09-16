# Test quality and completion audit — 2026-09-16

## Verdict and scope

The tests are not empty, but the previous green suite had meaningful blind spots. Deliberately breaking selected production behaviors detected 15 of 28 changes before strengthening assertions, versus 27 of the same 28 afterwards. Twelve previously missed behavioral faults now have failing-test witnesses. The remaining probe is kept visible and explained below; this is not a claim of 100% test quality.

Baseline checkout: `f7c15c5c8dd5c0b6232ade116cbf14bc7b188cd4`. Its Go sources/tests are identical to the source-bundle code snapshot `5f965cd757f8a9c8b9f43f18b59ea83b9e1ebe08`; the intervening commit changed documentation only. Verified strengthened checkout: `aaed98f174f43213be1f82d1ac3135520cdf0770`.

This audit changes tests, test tooling and CI, not the normal production Go code or database schema. Injected faults run only in temporary copies and were never pushed into application code. The twelve gaps are missing test sensitivity, NOT twelve defects found to exist in the unmodified bot. The ordinary application passes the new assertions without behavior changes.

## Are we finished?

| Area | Current evidence | Completion boundary |
|---|---|---|
| Single-owner Telegram workflows | Runnable daemon, real SQLite and fake-API integration: add, answer, callbacks, keyboard cleanup, daily delivery, management, restart | Initial functional implementation exists; no live Telegram validation in this audit |
| Independent forecasts and fixed reserve | Deterministic regression scenarios; cross-item cap injection is detected | Does not establish practical accuracy or low interaction burden |
| Forecast learning and seasons | Non-seasonal `interval-baseline-v1`; numeric contracts now tested more precisely | Seasonal learning, calibrated probabilities, pilot and comparison with fixed reminders are not implemented/validated |
| Operations | Backup command, migrations, shutdown, selected retry and race tests | Deployment packaging, health endpoint, backup/record retention and failed-job diagnostics remain incomplete |
| Interface breadth | Button onboarding, pause/archive/settings | Shared schedule edits, richer quantities, renaming/corrections and multiple users remain pending |

See [roadmap.md](roadmap.md). A functional first bot is not the same as a completed forecasting product or a production reliability certificate.

## How the tests were challenged

The original 114 named Go test functions were inspected using the Go syntax tree and manual review. No empty bodies, skip calls or TestMain success bypass were found; each named test contained a direct failure check. This syntactic check is weak evidence on its own: asserting an irrelevant result can still produce high coverage.

The stronger experiment applied 28 reviewed source changes, one at a time. They cover authorization, message binding, button generations, quantities, forecasts, due dates, transaction durability and external delivery. For each change the complete Go test suite ran with `-count=1` and JSON events. The unchanged suite had to pass first. All 28 changes compiled and executed tests in both completed campaigns; no compilation error or timeout was credited as a detected fault.

The initial runner recorded the before results. The persisted runner repeated the final campaign and additionally requires a named failing regression for each required mutation. Results are selected fault-injection sensitivity, not an exhaustive mutation score over the whole codebase or an estimate of the chance of missing future bugs.

## Complete before/after matrix

Detected means at least one test failed on the injected change; missed means the changed implementation still passed. Exact source anchors and final named witnesses are in [tests/mutations.json](../tests/mutations.json).

| ID | Injected change | Original tests | Strengthened tests |
|---|---|---|---|
| M01 | Accept callbacks from a foreign sender | Detected | Detected |
| M02 | Accept callbacks from a different message | Detected | Detected |
| M03 | Accept stale wizard generations | Detected | Detected |
| M04 | Accept actions never offered by the wizard | Detected | Detected |
| M05 | Silently skip keyboard-only removal | Detected | Detected |
| M06 | Serialize terminal keyboard as null instead of empty array | Missed | Detected |
| M07 | Treat unknown answer as fresh physical observation | Detected | Detected |
| M08 | Reset stock on purchase instead of adding | Detected | Detected |
| M09 | Learn consumption through unknown purchase history | Detected | Detected |
| M10 | Train lower rate bound from optimistic wrong endpoint | Missed | Detected |
| M11 | Train upper rate bound from wrong endpoint | Missed | Detected |
| M12 | Estimate exhaustion from upper stock bound | Missed | Detected |
| M13 | Misclassify replenishment as ordinary stock check | Missed | Detected |
| M14 | Slide observation-gap deadline on every tick | Detected | Detected |
| M15 | Offer future planned items as due | Missed | Detected |
| M16 | Introduce cross-item cap of eight questions | Detected | Detected |
| M17 | Commit partial changes despite transaction error | Detected | Detected |
| M18 | Save projection but omit original event | Detected | Detected |
| M19 | Disable SQLite foreign-key integrity | Missed | Detected |
| M20 | Replay with unsupported model version | Missed | Detected |
| M21 | Remove callback acknowledgement from real daemon | Missed | Detected |
| M22 | Accept no-use without confirmed history | Missed | Detected |
| M23 | Forget bot-wide flood cooldown | Detected | Detected |
| M24 | Use final reply time instead of physical check time | Missed | Detected |
| M25 | Map half-full preset to three-quarters | Missed | Detected |
| M26 | Allow stale stock at final onboarding confirmation | Detected | Detected |
| M27 | Restore archived item with notifications enabled | Detected | Detected |
| M28 | Remove lead time from redundant ordinary deadline only | Missed | Missed |

### The remaining M28 probe is not hidden

M28 removes shopping lead time from only the ordinary no-reserve deadline. The separate safety deadline still uses `max(0, stock.low / rate.high - lead_days)`.

For the validated nonnegative stock/rate intervals, `stock.low / rate.high <= mid(stock) / mid(rate)`. Therefore the safety candidate is no later than the ordinary candidate, and removing lead time from the ordinary candidate alone does not move the final check date. Stock and action outputs also remain unchanged for this change. The textual reason/tie-break can change, so this is NOT claimed to be fully equivalent behavior.

M28 survived both campaigns. The manifest retains it as an explicitly explained diagnostic probe, not a required kill. Reports continue to show **27 detected / 1 surviving**, rather than presenting a misleading 28/28 or silently dropping it. The 27 user-visible/safety-relevant seeded faults have required named witnesses.

## What was actually weak, and what now catches it

### Callback acknowledgment tested in the helper, not the daemon

`internal/bot/test_helpers_test.go` calls `AnswerNotice` itself. Counting those acknowledgments verifies the service harness and HTTP adapter, but cannot prove that `cmd/hoarder.run` performs the call. Removing the real daemon's `AnswerNotice` left the original suite green (M21).

New `TestDaemonAcknowledgesCallbacksAfterDurableDecision` drives the actual daemon loop through a fake HTTP transport. The helper never sends an acknowledgment. It checks the callback ID, acceptance/refusal text, cache setting, durable SQLite receipt before success acknowledgment, rejected foreign input, duplicate callback delivery and the final persisted item. A failed acknowledgment request must not undo the already committed answer.

`TestDaemonFailedCommitAcknowledgesFailureNotSuccess` installs a real SQLite trigger that rejects item creation. The real daemon must acknowledge failure, not success, with neither an item nor a success receipt committed. This checks ordering across the application/database/API boundary rather than just calling isolated functions.

### Empty Go slice length is not the JSON wire contract

The old keyboard helper accepted both a nil slice and an empty slice because both have length zero. M06 changed the outgoing `inline_keyboard` value from `[]` to `null`, and the old tests missed it.

The helper now rejects nil rows. The real-daemon test separately inspects the outgoing raw JSON and requires `reply_markup.inline_keyboard` to be the explicit empty array after completion. It does not infer wire behavior merely from the length of a decoded collection.

### Changed values are not necessarily correct values

The earlier learning test checked that a rate changed, so learning from the wrong interval endpoint still passed (M10/M11). The new test uses an independently hand-computed case: stock 2 to [1.4,1.6] over ten days, initial rate [0.03,0.04], weight 0.35, expected learned rate **[0.0335,0.047]**. Expected values are not obtained by calling the reducer again.

Unequal stock/rate bounds now test a concrete safety deadline: stock [1,3], rate [0.1,0.2], one day to buy -> check in four days. Action tests distinguish wait, evidence check and replenishment (M12/M13). Preset tests require all four exact stock intervals under both reserve policies, including half-full, not just successful creation or item count (M25).

### Missing negative cases and time semantics

New tests keep future items out of both `/today` and automatic delivery, then assert exactly one notification at the stored slot without changing physical evidence (M15). Delayed purchase-history confirmation must preserve the earlier physical-check timestamp (M24). Unconfirmed no-use must fail without mutating state (M22).

SQLite tests deliberately attempt an orphan event and require the actual foreign-key extended error; a valid-parent append must still succeed (M19). Replay must refuse an unknown model version (M20). These were previously unchallenged guards.

The already useful tests continue to catch duplicate/addition mistakes, unknown-as-no-use assumptions, stale/foreign callbacks, rollback violations, missing event history, a global item cap and archive restoration that silently resumes notifications. No old tests were disabled or weakened.

## Repeatable gate, not just a one-off report

```sh
make check
make mutation
# Focused investigation:
python3 scripts/mutation_check.py --only M21,M06
```

Python 3 is required for the audit tooling, not for running the bot. `make check` includes nine Python tests of the runner itself. `make mutation` copies only approved source/module paths to a disposable directory, changes production files only, restores each before the next case, and emits `mutation-results/report.json` plus baseline/per-case logs. The source checkout is fingerprinted before/after and is not modified.

A required mutant must fail its declared test witness. Build/setup errors, timeouts, no-test runs, stale/ambiguous source anchors and unrelated failed tests do not satisfy that requirement. The runner itself has tests for those classifications and path/manifest validation. A renamed or refactored source anchor fails the gate until deliberately reviewed; it is not silently skipped.

The GitHub `Go checks` workflow includes `make mutation` after the normal checks/build. This is regression protection for the reviewed 27 faults, not universal proof against every possible implementation change.

## Verification and evidence

Local environment: Linux amd64, Go 1.23.2, GCC/CGO and checksum-verified offline vendor dependencies; compatibility testing only, not a production toolchain recommendation. Normal runtime source files were compared byte-for-byte with the pre-audit copy and are unchanged.

| Check | Result |
|---|---|
| Original unchanged suite | Passed, 114 named Go tests / three fuzz targets |
| Original 28-change campaign | 15 detected, 13 survived, no invalid/timeout results |
| Final unchanged suite | Passed, 125 named Go tests / three existing fuzz targets |
| Final 28-change campaign | 27 detected with required witnesses, M28 survived as disclosed |
| Runner self-tests | Nine passed |
| `make check` | Formatting, vet, race tests and runner self-tests passed |
| New daemon/due-date/timestamp tests, 20 repetitions with race detector | Passed |
| Binary build/help and offline simulation | Passed locally with `-buildvcs=false` for the extracted source directory |

A copied timestamp fixture initially contained one extra zero in the GitHub write and failed `go vet` in CI. It was corrected in `aaed98f`; the downloaded published sources and the audited local production/test files were then compared. That failed intermediate CI run is not counted as success.

Final local manifest SHA-256: `89f40fda78ae819b2a94d5e62089d80137c3d626176540826c8cc7d21b110fa7`.

Final production+tests+module fingerprint, using the runner's documented algorithm: `381f66c295fa9e8d84df864267b63c78fe1e19b2cc4cef3ceafce119a5290879`.

The forecast package's statement coverage stayed **95.2% before and after**, despite the old suite missing the deliberately wrong safety calculation and action. This is a direct example of why line/statement coverage was not used as the quality verdict.

[GitHub Actions run 35068758136](https://github.com/EugenSleptsov/hoarder/actions/runs/35068758136), job `104705045876`, completed successfully on `aaed98f174f43213be1f82d1ac3135520cdf0770`. It passed dependency verification, formatting, vet/race tests, runner self-tests, executable build/help, simulation and the full named-witness mutation gate. Workflow environment: Ubuntu 24.04, Go 1.27.1, CGO enabled.

## Remaining uncertainty

These are selected mutations, not a whole-program operator sweep, and the new cases were chosen after inspecting the initial survivors. Detection of these faults is concrete regression evidence, not independent statistical validation of all tests. A passing scenario proves neither production availability nor the usefulness of a forecast to a household.

Live Telegram rendering, deployment, destructive power-loss recovery, arbitrary multi-process execution and a household pilot were not performed. The fixed response-size limit and first-send/commit duplicate window remain. Richer per-item forecasting and lower interaction burden require separate implementation and measurement. The bot is not declared finished by this audit.
