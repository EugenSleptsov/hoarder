# Forecasting contract and baseline

## Scope

`interval-baseline-v1` is a transparent reference implementation, not a claim of validated forecasting accuracy. It has no annual seasonal component, inferred purchase-delay distribution, calibrated stockout probabilities or optimal value-of-information policy. Its purpose is to make the product invariants executable before adding a more complex model.

Input: one item state and an explicit time. Output: plausible current stock, an absolute next-check deadline, the reason for that deadline and a suggested **question**, not a purchase or automatic order.

## Units and observation semantics

One unit is a stable standard package equivalent for this item. Reserve size `B` is fixed configuration. Total stock `Q=[q_low,q_high]` is separate from reserve policy. A purchase of `a` units adds `[a,a]` to projected stock, never resets it to a target.

A proposed UI mapping is empty -> `[0,0]`, 25% -> `[0.125,0.375]`, 50% -> `[0.375,0.625]`, 75% -> `[0.625,0.875]`, full -> `[0.875,1]`. Add confirmed unopened units to this range. These intervals are UX assumptions, not objective measurement precision. If the amount cannot be bracketed reliably, accept unknown instead of inventing a narrow range. Multiple packages and sizes require explicit unit conversion.

An answer `empty` means empty at observation time; the actual depletion may have happened earlier. A reserve-absent answer alone is not an exact total quantity and must not be passed to the reducer as if it were one.

## Projection

For rate range `R=[r_low,r_high]` and elapsed days `d` from the stock anchor:

```text
Q_low(t)  = max(0, q_low  - r_high * d)
Q_high(t) = max(0, q_high - r_low  * d)
```

Days here are elapsed 24-hour periods; local calendar scheduling is a different layer. Bounds widen under uncertain consumption. Projection does not update the last actual observation, fit a new rate or imply that a question was answered.

The midpoint of a plausible range is a baseline heuristic, not a fitted statistical expectation. Bounds are conditional on the configured/learned plausible rate range. Shocks outside that range remain possible.

## Learning

The baseline learns only from two current-stock snapshots at least one elapsed day apart when:

- the respondent confirms no unreported additions over the entire interval;
- there were no recorded additions either;
- the new stock lower bound is positive (no observed/could-be-empty censoring);
- the new stock range does not imply an impossible increase under that history.

This deliberately excludes even some usable purchase intervals because an intervening stockout can make unconstrained demand unidentifiable. A later model may use interval-censored likelihoods and explicitly confirmed continuous availability; the baseline does not pretend to implement them.

```text
sample_low  = max(0, previous_low  - current_high) / d
sample_high = max(0, previous_high - current_low)  / d
new_low     = 0.65 * old_low  + 0.35 * sample_low
new_high    = max(1e-9, 0.65 * old_high + 0.35 * sample_high)
```

The smoothing coefficient and tiny numerical rate floor are implementation choices to evaluate, not proven optima. The floor prevents a permanent zero-rate assumption. Wide/coarse observations can leave significant uncertainty.

Unknown purchase history: re-anchor current stock, leave rate unchanged. Empty stock: re-anchor to zero, do not infer that the last package lasted exactly until the reply. An unexplained increase is not automatically a recorded purchase. An explicitly confirmed no-use interval preserves stock over that interval but does not set the future consumption rate to zero.

A missed answer produces no event. `unknown` is an explicit contact with no physical stock evidence; the inventory keeps depleting and the maximum physical-observation gap is not reset.

## Two boundaries and a check deadline

At the stored stock anchor, calculate:

```text
with reserve:
  ordinary_check_delay = max(0, (mid(Q) - B) / mid(R))

without reserve:
  ordinary_check_delay = max(0, mid(Q) / mid(R) - lead_days)

safety_check_delay = max(0, Q_low / R_high - lead_days)

next_check = earliest of:
  anchor time + ordinary_check_delay
  anchor time + safety_check_delay
  last physical observation + max_check_days
```

The reserve serves as leeway between ordinary replenishment and total exhaustion. If uncertainty makes exhaustion possible before the ordinary check, the safety deadline brings the question forward. The algorithm never modifies `B`.

The deadline is absolute and anchored to evidence. Re-running a scheduler tomorrow must not transform 'check in ten days' into a fresh ten-day delay. Explicit unknowns must not indefinitely reset the evidence gap.

The baseline returns `check_replenishment` when an already-due item's central stock estimate is at/below the reserve boundary, or exhaustion before replenishment is plausible. This means ask/verify, not add a confirmed purchase or issue an order. A durable application must maintain an open purchase intent and schedule its confirmation separately so the same recommendation is not recreated each day.

## Mapping to the daily window

Map the desired deadline to the preceding configured local-time slot when that slot is still available. If it is already past, use the next slot and mark the desired deadline as unmet. Do not quietly claim that rounding to tomorrow preserves the safety margin.

The schedule depends only on this item and the shared timezone/hour. A dispatcher may paginate due questions, but may not defer one because another item's question consumed a shared budget.

## Seasonal extension: design, not implemented

Keep all parameters per item. Candidate extension: a positive local rate plus a shrinkage-controlled annual periodic component, with a separate change-point/temporary-shock component. Start with strong shrinkage toward no seasonal effect. A single summer or sparse annual observations cannot distinguish seasonality from a one-off event.

Require repeated seasonal coverage and better rolling-origin performance than the simpler model before activating a seasonal component for an item. No global category training or pooled household factors under the independence requirement. A manually provided initial prior would be explicit configuration, not claimed as learned history.

Intermittent items may need an event-probability model and an amount-per-event model rather than continuous depletion. A finite maximum observation gap remains useful for detecting changes, but its question burden must be measured rather than hidden.

## Evaluation and known limitations

Regression scenarios must include stable use, faster use, hidden replenishment, explicit additions, unanswered prompts, stockouts between checks, startup without history, long non-use, annual patterns, irregular demand and a shock larger than the reserve. Seasonal and shock scenarios can expose a baseline failure; they must not be labelled supported simply because a test runs.

Track late replenishment prompts even if the reserve prevents a stockout. Also track unnecessary questions and early purchase recommendations. Compare with a fixed per-item reminder schedule using the same fixed physical reserve. Never improve reported availability by automatically increasing inventory.

Open limitation: a baseline may repeatedly have a due question after `unknown` while risk or the maximum gap remains unresolved. A per-item reminder/backoff policy is an application task; it may not freeze physical consumption or use other items as a reason to defer. Its safety/annoyance trade-off requires a pilot.
