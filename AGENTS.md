# Working on Hoarder

Read `docs/requirements.md`, `docs/architecture.md`, `docs/forecasting.md` and `docs/roadmap.md` before changing behaviour. The README and roadmap must describe actual implementation status, not just target design.

## Product invariants

1. Prediction is primary; a fixed physical reserve is leeway. Never automatically raise reserve size or a stock target to compensate for a model error.
2. Each item is independent. No other item's data, learned parameters, ranking, question volume or shopping state may affect this item's forecast or question date.
3. The only cross-item coordination is the explicitly configured daily delivery window and presentation of due questions together. No hidden global question cap.
4. The user is not a manual inventory clerk. Do not require reporting each consumption, purchase or reserve opening as the primary workflow.
5. Missing reply, unknown, confirmed whole-household no-use, current snapshot and quantity addition are distinct observations.
6. A purchase recommendation is not a purchase. Confirmed additions add to remaining stock exactly once; they do not reset stock to a target.
7. Unknown purchase history must not generate a fake consumption sample. Reply time is not automatically a depletion/purchase/transition time.
8. The baseline is non-seasonal and its intervals are heuristic. Do not claim seasonal learning, optimal question timing, calibrated stockout probabilities or measured real-world accuracy without implementing and validating them.

## Engineering rules

- Keep the item reducer and predictor pure: explicit input time, no global random state, I/O or other items. Retain model versions for replay.
- Validate quantities, dates, revisions, command identity and ownership at the relevant boundaries.
- Durable state changes require transactions and uniqueness; in-memory duplicate handling alone is not enough.
- Persist selected question dates. Do not re-plan an overdue question on every tick so it is always moved to the next future slot.
- Separate item evidence, question status, purchase intent and external delivery. A sent prompt is not an observation.
- Never claim exactly-once Telegram sends across the send/commit crash window.
- Run tests against temporary databases and fake HTTP servers. Do not send live bot messages as part of tests.
- Never commit tokens, credentials, live chat histories or household database files. Do not wrap/log token-bearing HTTP URL errors.
- Preserve existing and concurrent changes. Use small coherent commits and send them to the remote frequently when requested. Do not force-update `main` or discard someone else's commit to resolve a non-fast-forward error.
- A planned interface, schema or conversation is not implemented functionality. Update the roadmap when a vertical slice actually passes its acceptance tests.

## Verification

```sh
gofmt -w ./cmd ./internal
go vet ./...
go test -race -count=1 -cover ./...
go run ./cmd/hoarder-sim
```

`make check` covers formatting, vet and race tests. Use a supported Go toolchain for network-facing deployment; the module's minimum language version is a compatibility floor, not an operational recommendation. No external Go module dependencies are required by the initial reference core.

When changing scheduling, test DST, restart/catch-up and overdue persisted plans. When changing observations, test replay, duplicates, stale revisions, unknown additions, censored empty answers and unchanged reserve configuration. When changing models, test complete item independence and compare only against information available at the forecast time.
