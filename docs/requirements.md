# Hoarder — product requirements

Status: design baseline, 2026-09-15. Source: the owner's product discussion.

## Goal

Keep ordinary household consumables available with minimal human attention. The user does **not** maintain an inventory ledger, report every use, or remember to announce opening a package. The bot predicts when clarification or replenishment becomes useful and asks at that time.

Prediction is the primary mechanism. A physical reserve is **leeway for prediction error, delayed answers and shopping**, not a replacement for prediction.

## Non-negotiable invariants

1. Each item is an independent model. Item A's history, rate, confidence, seasonality, purchase delays and next question date must never depend on item B. Removing other items must leave A's results unchanged.
2. Items have a `with_reserve` property. A reserve has a fixed, explicitly configured size; the algorithm must never increase a stock target or reserve to conceal inaccurate predictions.
3. Desired reserve policy and estimated reserve presence are different facts.
4. The only cross-item coordination is the household's daily delivery time and presentation of the due questions together. No cross-item inference, shared question budget, prioritisation-based deferral, shared shopping calendar or inferred household consumption multiplier.
5. Each item chooses its own next question date. At the common local-time window, the delivery layer collects due items. No due items means silence. No item is deferred because another item is also due.
6. No obligation to report opening a reserve, every purchase or every use spontaneously. Bot-initiated snapshots are the primary interaction. Optional proactive answers are a convenience, never a correctness assumption.
7. A recommendation to purchase is not a purchase. A purchase adds stock; it does not reset the current open package to full.
8. An unanswered question, an explicit `unknown`, and confirmed `not used` are distinct observations. None may be silently reinterpreted as another.
9. Coarse answers are uncertain observations, not exact measurements. Response time is not automatically the time an earlier depletion or reserve transition happened.
10. All durable changes are idempotent. Retries and repeated button presses must not duplicate stock additions or learning observations.
11. The bot recommends; it never places or pays for orders in this scope.

## Item concepts

- Item: one independently modelled consumable at one household; e.g. toothpaste.
- Unit: a stable package equivalent, chosen per item. Changing package size requires explicit conversion, not implicit equivalence.
- Active quantity: approximate total quantity in currently used packages, expressed in standard units.
- Closed reserve quantity: approximate unopened units physically present.
- Total quantity: active plus closed. More than one active package is possible; a UI may ask for a combined rough estimate.
- Reserve policy: disabled or a fixed buffer quantity. A boolean with a default one-package reserve is sufficient for onboarding; configuration may express another fixed size.
- Replenishment boundary: estimated moment a reserve starts being consumed, or, without reserve, the moment to act before total depletion accounting for response/shopping lead time.
- Exhaustion boundary: estimated moment total usable stock runs out. These two boundaries must not be conflated.
- Forecast uncertainty: epistemic uncertainty from coarse/missing observations and possible demand changes. Do not label heuristic intervals as calibrated probabilities.

## Initial interaction

First transport assumption: Telegram, with a transport-independent application core.

Onboarding asks for name, reserve yes/no and a rough current quantity. A rough usual package duration is optional but useful for cold start. Without history or a duration hint, use an explicit broad configurable prior and say that the model is learning, not that the forecast is established.

Examples of bot-initiated questions:

- `Is there still an unopened spare?` yes / no / not checked.
- `Roughly how much remains in the open package?` full / 75% / 50% / 25% / empty / unknown.
- `Already replenished since the previous check?` yes / no / don't remember.
- `The item is already on the shopping list. Has it been bought?`

Ask a follow-up only when needed to interpret an answer or change an action. `Not used` must refer to all household use over a stated interval; `I did not personally use it` cannot establish that.

## What the algorithm may and may not infer

Consumption between snapshots is identifiable only when intervening replenishment is known sufficiently well. Same stock at two checks can mean no use, or heavy use plus a hidden purchase. An ambiguous interval can re-anchor the present stock estimate but cannot safely train the rate.

If a reserve was present at t1 and absent at t2, an observed transition is interval-censored, not known to occur at t2. If intervening replenishment is uncertain, even the single-transition interpretation may be invalid.

Seasonality is per-item. A rate change is not evidence of a yearly pattern. Annual seasonality needs repeated sufficiently observed cycles and out-of-sample evaluation. The initial implementation may use conservative non-seasonal forecasting, provided this limitation is explicit. Never claim to have learned seasons from a short history.

Sudden demand cannot be guaranteed predictable from sparse historical snapshots. A reserve absorbs some errors but has finite capacity. Without a reserve, an item that can exhaust between daily checks cannot be guaranteed available.

## Daily delivery

One configured IANA timezone and wall-clock time per household. Model timestamps are stored in UTC; calendar dates are interpreted in that timezone. Handle DST, restarts, duplicate scheduler ticks, missed windows, Telegram delivery failures and delayed replies explicitly.

One daily session can contain multiple independently due questions, presented as an editable card / paged list. Asking the next already-due question following an answer is user interaction within the same session, not a newly scheduled notification. Unanswered items continue to age; they are not marked as observed merely because a session was created.

Strict independence means there is no guarantee of a small maximum number of questions per day. Minimise each item's question rate; do not hide aggregate burden with an undocumented global cap.

## Persistence, security and operations

Persist item settings, immutable observations, forecasts with algorithm version, scheduled question state, purchase intent, delivery state and Telegram update cursor. Never commit bot tokens or household observations. Restrict access to an explicitly configured chat; validate callback ownership, freshness and version. Back up the database; support replay/inspection before adopting a more complex predictor.

A database transaction cannot atomically include an external Telegram request. Delivery guarantees and crash windows must be documented; do not claim exactly-once messages where the API does not supply that property.

## Acceptance criteria

- Adding/removing/mutating B leaves A's forecast and next-check date unchanged.
- Reserve size never changes as a consequence of observations, missed predictions or retries.
- Missing answers continue stock depletion and never create purchases.
- Confirmed replenishment adds to the existing estimate exactly once.
- Repeated callback and scheduler processing are harmless.
- Changed current stock with unknown purchase history updates stock but does not create a spurious slow/fast consumption sample.
- Same forecast inputs and algorithm version produce the same result.
- A question sent at the daily window does not create an exact observation at send time.
- Daily delivery is tested around Europe/Berlin spring and autumn DST transitions.
- No recommendation is interpreted as authorisation for a financial transaction.

## Evaluation

Compare against a per-item fixed reminder baseline. Measure answers and physical inspections per item-month, early/unnecessary purchase suggestions, missed replenishment boundaries, reserve usage caused by late prompts, and actual stockouts. A reserve can hide an inaccurate predictor, so stockouts alone are insufficient.

Synthetic scenarios are regression tests, not evidence of real-world forecast accuracy. Use rolling-origin evaluation on observed history without future leakage; a retrospective dataset cannot reveal what a user would have answered on dates that were never asked.

## Initial scope and exclusions

Start with one household and ordinary non-perishable consumables. No medical-critical stock guarantees, perishable expiry modelling, automatic ordering, receipt/email/calendar integrations, LLM dependency, cross-item learning, or automatic reserve optimisation. These are explicit scope boundaries rather than silently implemented assumptions.
