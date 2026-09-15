# Telegram interaction design

Status: application flow specification. The repository's HTTP adapter does not yet implement these conversations or persist sessions.

## Principle

A household member should answer the bot's occasional question, not maintain a list of daily uses. Optional commands must not become an implicit obligation. All proactive questions use the shared local-time window. Button-driven follow-ups remain inside the same already-open daily session.

## First setup

Owner configures a bot token outside Git, an authorised chat, IANA timezone and local hour. `/start` in any other chat cannot claim ownership. Ask once for each item: name, stable package unit, reserve yes/no, rough current state. Optionally ask whether one package normally lasts days, weeks or months; allow unknown. Advanced settings hold a fixed reserve size and per-item response/shopping allowance.

Avoid asking for a precise opening date, receipt history or a percentage every day. The first rate is a labelled prior, not learned household behaviour.

## Daily session

Example in Russian:

```text
Сегодня стоит проверить 2 вещи.

1/2. Зубная паста
По прогнозу пора проверить запас.
Запасной закрытый тюбик ещё есть?

[Есть] [Нет] [Не смотрел]
```

The count includes every independently due question; there is no global cap that postpones some items. Use paging / editing one message rather than independent notifications. A user can select another due item without changing either item's scheduled date.

When the reserve is confirmed and the action would not change, avoid an unnecessary precise follow-up. A future observation-likelihood adapter can use this partial observation directly. The current baseline accepts total quantity intervals, so the initial wired implementation must either ask enough to bracket the total or pass a deliberately broad interval. It must not invent a precise stock level from reserve presence alone.

If the reserve is absent and urgency is unclear:

```text
В открытом тюбике примерно сколько?

[Полный] [¾] [½] [¼] [Пусто] [Не знаю]
```

Without reserve, start with the current-quantity question. Before using the interval for rate learning, clarify whether any unreported replenishment occurred since the displayed previous check date. Unknown history is acceptable: update current stock without training that interval.

This may require two or three taps in the baseline. Reducing physical inspections and taps is a pilot metric, not a property to claim before measuring it.

## Replenishment ambiguity

`Уже купили` means a purchase happened at an unknown time, not that a new full package arrived at the exact reply time. Prefer asking for the present state and recording unknown purchase history. If the user explicitly says `Купил сейчас одну упаковку`, the optional addition command can add one unit at the current time.

A purchase recommendation opens one per-item shopping intent. It does not add stock. Once an intent exists, the next relevant interaction is usually confirmation, not recreating the same recommendation.

```text
Паста уже в списке покупок. Запас пополнили?
[Да] [Ещё нет] [Не помню]
```

If yes but timing/quantity is unclear, re-anchor to a current snapshot. A false precision fix here would corrupt the rate model over repeated cycles.

## Unknown and no-use

`Не смотрел / Не знаю` skips the physical observation and keeps stock projection running. It is not `ничего не израсходовали`.

`Не использовали` is offered only with an explicit interval and wording covering the whole household. It cannot mean merely that the person answering did not personally use the product. The pure core requires an exactly identified interval and confirmed history for this event.

No response creates no stock event at all. Store delivery/response status separately from item observations. A maximum check gap may remain overdue; reminder/backoff policy must be per item and must not erase that uncertainty.

## Stale buttons and shared households

A question binds to household, item, session, expected revision and valid actions. Validate all of these server-side. Use an opaque compact identifier in callback data, not a trusted arbitrary item ID from the client.

After another member has already answered, old buttons return `Этот вопрос уже обновлён` rather than overwrite the newer state. Double taps return the previous result without another quantity addition. A newer question can supersede the old question; the older message must not remain an active mutation path.

A delayed reply normally describes current observed state. The application must supply its actual observation time, not the original notification time. User-supplied historical corrections require event replay rather than appending old physical changes after current state.

## Proposed user commands

`/add`, `/items`, `/shopping`, `/check`, `/settings`, `/pause`, `/resume`, `/help`. An optional `/bought` is a convenience only. Commands are planned, not implemented by the HTTP client.

`/check` is explicitly requested interaction and can be used outside the scheduled hour. It does not shift another item's model or create extra automatic notification times.

## Safety and operational rules

Never automatically order, pay, scrape private receipts or connect a calendar. Medical-critical availability and perishable expiry are outside the initial scope. No secrets in screenshots, logs, example configuration or source control.

Persist callback results before acknowledging business success. Telegram callback acknowledgement and editing a message are separate transport operations. The outbox repairs message presentation after a crash; receipt deduplication protects domain state. A lost send acknowledgement can still cause a duplicate message, as described in `architecture.md`.

## Transport API references

Telegram Bot API: https://core.telegram.org/bots/api

Relevant constraints include callback data of 1–64 bytes, callback acknowledgements, explicit polling offsets, mutually exclusive polling/webhooks, and `retry_after` on flood-control errors. The adapter tests these transport contracts with a local HTTP server, not with a live bot token.
