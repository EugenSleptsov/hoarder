package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

const memberChat = 456

// namedChat decodes a chat as Telegram sends it, so it carries a display name.
func namedChat(t *testing.T, id int64, name string) telegram.Chat {
	t.Helper()
	var c telegram.Chat
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"id":%d,"type":"private","first_name":%q}`, id, name)), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// invite walks the owner's flow and returns the code from the link.
func (h *harness) invite() string {
	h.t.Helper()
	h.command("/family")
	h.press("Пригласить")
	text := h.wire.snapshot().Text
	i := strings.Index(text, "https://t.me/hoarder_test_bot?start=")
	if i < 0 {
		h.t.Fatal("no invitation link", text)
	}
	i += len("https://t.me/hoarder_test_bot?start=")
	return text[i : i+24]
}

// open sends what Telegram sends when someone opens an invitation link.
func (h *harness) open(chat int64, name, code string) {
	h.t.Helper()
	h.sequence++
	h.process(telegram.Update{ID: h.sequence, Message: &telegram.Message{ID: h.sequence, Date: h.now.Unix(), Chat: namedChat(h.t, chat, name), Text: "/start " + code}})
}

func (h *harness) join(chat int64, name string) {
	h.t.Helper()
	h.open(chat, name, h.invite())
	if !strings.Contains(h.wire.snapshotIn(chat).Text, "Вы подключены") {
		h.t.Fatal("member not welcomed", h.wire.snapshotIn(chat))
	}
}

// buttonOn taps a button of an older message in chat.
func (h *harness) buttonOn(chat int64, msg telegram.Text, label string) telegram.Update {
	h.t.Helper()
	for _, row := range msg.Keyboard.Rows {
		for _, b := range row {
			if b.Text == label {
				h.sequence++
				return telegram.Update{ID: h.sequence, Callback: &telegram.Callback{ID: strconv.FormatInt(h.sequence, 10), From: telegram.User{ID: chat}, Message: &telegram.Message{ID: msg.MessageID, Date: h.now.Unix(), Chat: telegram.Chat{ID: chat, Type: "private"}, Text: msg.Text}, Data: b.Data}}
			}
		}
	}
	h.t.Fatalf("button %q missing from %+v", label, msg)
	return telegram.Update{}
}

func hasButton(msg telegram.Text, label string) bool {
	if msg.Keyboard == nil {
		return false
	}
	for _, row := range msg.Keyboard.Rows {
		for _, b := range row {
			if b.Text == label {
				return true
			}
		}
	}
	return false
}

func (h *harness) flush() {
	h.t.Helper()
	if err := h.service.Flush(testContext, h.api, h.now); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) isMember(chat int64) bool {
	h.t.Helper()
	found := false
	err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		var m member
		err := tx.Get(testContext, "member", chatKey(chat), &m)
		found = err == nil
		if errors.Is(err, sqlstore.ErrNotFound) {
			return nil
		}
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return found
}

func TestInvitedMemberSharesTheHouseholdFromTheirOwnChat(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.commandIn(memberChat, "/items")
	if h.wire.sendsTo(memberChat) != 0 || h.isMember(memberChat) {
		t.Fatal("a stranger got an answer")
	}
	code := h.invite()
	if text := h.wire.snapshot().Text; !strings.Contains(text, "одноразовое и действует до 16.09.2026 20:00 (Europe/Berlin)") {
		t.Fatal("invitation does not say it is one-time and when it ends", text)
	}
	h.open(memberChat, "Мария", code)
	if !h.isMember(memberChat) || !strings.Contains(h.wire.snapshotIn(memberChat).Text, "Вы подключены") {
		t.Fatal("member not admitted", h.wire.snapshotIn(memberChat))
	}
	if text := h.wire.snapshot().Text; !strings.Contains(text, "Мария подключается к семье") {
		t.Fatal("owner not told", text)
	}
	before := h.state("Паста")
	h.commandIn(memberChat, "/items")
	h.pressIn(memberChat, "Паста")
	h.pressIn(memberChat, "Нет")
	h.pressIn(memberChat, "50%")
	after := h.state("Паста")
	if after.Revision != before.Revision+1 || after.Stock.High > 1 {
		t.Fatal("a member's answer did not update the shared item", after)
	}
	h.command("/family")
	text := h.wire.snapshot().Text
	if !strings.Contains(text, "Владелец (вы) — ежедневные вопросы включены") || !strings.Contains(text, "• Мария, с 15.09.2026 — ежедневные вопросы включены") {
		t.Fatal(text)
	}
	h.commandIn(memberChat, "/family")
	mine := h.wire.snapshotIn(memberChat)
	if !strings.Contains(mine.Text, "• Мария (вы)") || hasButton(mine, "Пригласить") || hasButton(mine, "Убрать: Мария") {
		t.Fatal("a member sees owner controls", mine)
	}
}

func TestMessageIDsAreScopedPerChat(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.add("Мыло", true, "Одна", "Полная")
	h.join(memberChat, "Мария")
	h.command("/items")
	owner := h.wire.snapshot()
	// Telegram counts message IDs per chat: give the member the same number.
	h.wire.mu.Lock()
	h.wire.next[memberChat] = owner.MessageID - 1
	h.wire.mu.Unlock()
	h.commandIn(memberChat, "/items")
	if got := h.wire.snapshotIn(memberChat).MessageID; got != owner.MessageID {
		t.Fatal("fake did not align message IDs", got, owner.MessageID)
	}
	h.press("Паста")
	if notice := h.process(h.buttonIn(memberChat, "Мыло")); notice != "" {
		t.Fatal("the owner's screen took over the member's message", notice)
	}
	mine, theirs := h.wire.snapshotIn(memberChat), h.wire.snapshot()
	if !strings.Contains(mine.Text, "Мыло") || !hasButton(mine, "Нет") || !strings.Contains(theirs.Text, "Паста") || !hasButton(theirs, "Нет") {
		t.Fatal("a chat lost its keyboard", mine, theirs)
	}
	h.pressIn(memberChat, "Нет")
	h.press("Нет")
	if !hasButton(h.wire.snapshotIn(memberChat), "50%") || !hasButton(h.wire.snapshot(), "50%") {
		t.Fatal("answers in one chat broke the other")
	}
}

func TestPendingTextInputsBelongToTheirChat(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.join(memberChat, "Мария")
	h.command("/add")
	h.commandIn(memberChat, "Шампунь")
	if !strings.Contains(h.wire.snapshotIn(memberChat).Text, "Команды:") {
		t.Fatal("the owner's name prompt took the member's text", h.wire.snapshotIn(memberChat))
	}
	h.command("Мыло")
	if !strings.Contains(h.wire.snapshot().Text, "Мыло") || !hasButton(h.wire.snapshot(), "С резервом") {
		t.Fatal("the owner's own name was lost", h.wire.snapshot())
	}
	h.press("Отмена")
	h.commandIn(memberChat, "/manage")
	h.pressIn(memberChat, "Паста")
	h.pressIn(memberChat, "Переименовать")
	h.command("Зубная паста")
	if h.state("Паста").Config.Name != "Паста" {
		t.Fatal("the member's rename prompt took the owner's text")
	}
	h.commandIn(memberChat, "Зубная паста")
	if h.state("Зубная паста").Config.Name != "Зубная паста" {
		t.Fatal("the member's rename failed")
	}
}

func TestInvitationsAreOneTimeExpireAndOnlyTheOwnerIssuesThem(t *testing.T) {
	h := newHarness(t)
	code := h.invite()
	h.command("/start " + code)
	if text := h.wire.snapshot().Text; !strings.Contains(text, "перешлите") || !strings.Contains(text, "не израсходовано") {
		t.Fatal("owner opening their own link", text)
	}
	h.open(memberChat, "Мария", code)
	h.open(789, "Пётр", code)
	if h.isMember(789) || !strings.Contains(h.wire.snapshotIn(789).Text, "уже использовано") {
		t.Fatal("an invitation was used twice", h.wire.snapshotIn(789))
	}
	h.open(790, "Чужой", strings.Repeat("0", 24))
	h.open(790, "Чужой", "x")
	if h.wire.sendsTo(790) != 0 || h.isMember(790) {
		t.Fatal("a stranger with a made-up code got an answer")
	}
	expired := h.invite()
	h.now = h.now.Add(inviteTTL)
	h.open(791, "Опоздавший", expired)
	if h.isMember(791) || !strings.Contains(h.wire.snapshotIn(791).Text, "устарело") {
		t.Fatal("an expired invitation admitted someone")
	}
	revoked := h.invite()
	h.command("/family")
	h.press("Отозвать приглашения")
	h.open(792, "Отозванный", revoked)
	if h.isMember(792) {
		t.Fatal("a revoked invitation admitted someone")
	}
	for i := 0; i < maxInvites; i++ {
		h.invite()
	}
	h.command("/family")
	if notice := h.process(h.button("Пригласить")); notice != "Слишком много приглашений." {
		t.Fatal("unbounded open invitations", notice)
	}
	// Even a button that should never reach a member is refused.
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		return h.service.note(testContext, tx, "Подделка", []action{{Label: "Пригласить", Kind: "invite"}, {Label: "Отозвать", Kind: "revoke"}}, place{Chat: memberChat}, h.now)
	}); err != nil {
		t.Fatal(err)
	}
	h.flush()
	if notice := h.process(h.buttonIn(memberChat, "Пригласить")); notice != "Это может только владелец." {
		t.Fatal("a member issued an invitation", notice)
	}
}

func TestRemovedMemberLosesAccessAndOldButtons(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.join(memberChat, "Мария")
	h.commandIn(memberChat, "/items")
	h.pressIn(memberChat, "Паста")
	question := h.buttonIn(memberChat, "Нет")
	h.commandIn(memberChat, "/add")
	h.command("/family")
	h.press("Убрать: Мария")
	stale := h.wire.snapshot()
	h.command("/family")
	h.press("Убрать: Мария")
	h.press("Да, убрать: Мария")
	if h.isMember(memberChat) || !strings.Contains(h.wire.snapshot().Text, "Мария больше не имеет доступа") {
		t.Fatal("member not removed", h.wire.snapshot())
	}
	before := h.state("Паста")
	if notice := h.process(question); !strings.Contains(notice, "Нет доступа") || !reflect.DeepEqual(before, h.state("Паста")) {
		t.Fatal("a removed member still answers", notice)
	}
	if msg := h.wire.messageIn(memberChat, question.Callback.Message.ID); msg.Keyboard == nil || len(msg.Keyboard.Rows) != 0 {
		t.Fatal("the removed member's question keeps its buttons", msg)
	}
	sent := h.wire.sendsTo(memberChat)
	h.commandIn(memberChat, "/items")
	h.commandIn(memberChat, "Шампунь")
	if h.wire.sendsTo(memberChat) != sent {
		t.Fatal("a removed member still gets answers")
	}
	// The item's question is free again; nothing of the member's stays active.
	h.command("/items")
	h.press("Паста")
	h.press("Нет")
	if !hasButton(h.wire.snapshot(), "50%") {
		t.Fatal(h.wire.snapshot())
	}
	h.join(memberChat, "Мария")
	if notice := h.process(h.buttonOn(ownerChat, stale, "Да, убрать: Мария")); notice != "Участник уже отключён." || !h.isMember(memberChat) {
		t.Fatal("an old confirmation removed a new membership", notice)
	}
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		return h.service.note(testContext, tx, "Только участнику", []action{{Label: "Все предметы", Kind: "view", View: "all"}}, place{Chat: 789}, h.now)
	}); err != nil {
		t.Fatal(err)
	}
	h.flush()
	if h.wire.sendsTo(789) != 0 {
		t.Fatal("buttons went to a chat outside the family")
	}
}

func TestDailyQuestionsGoToEachMemberWhoWantsThem(t *testing.T) {
	h := newHarness(t)
	// Europe/Berlin leaves summer time on 2026-10-25: the slot moves to 20:00 UTC.
	h.now = time.Date(2026, 10, 24, 17, 0, 0, 0, time.UTC)
	h.add("Паста", false, "Нет", "Пусто")
	h.add("Мыло", false, "Нет", "Пусто")
	h.join(memberChat, "Мария")
	h.join(789, "Пётр")
	h.commandIn(789, "/family")
	h.pressIn(789, "Выключить мои ежедневные вопросы")
	tick := func() {
		t.Helper()
		if err := h.service.Tick(testContext, h.now); err != nil {
			t.Fatal(err)
		}
		if err := h.service.Flush(testContext, h.api, h.now); err != nil {
			t.Fatal(err)
		}
	}
	owner, maria, petr := h.wire.sendsTo(ownerChat), h.wire.sendsTo(memberChat), h.wire.sendsTo(789)
	h.now = time.Date(2026, 10, 24, 19, 0, 0, 0, time.UTC)
	tick()
	tick()
	h.reopen()
	tick()
	if h.wire.sendsTo(ownerChat) != owner+1 || h.wire.sendsTo(memberChat) != maria+1 || h.wire.sendsTo(789) != petr {
		t.Fatal("digest not sent once to each wanting member", h.wire.sendsTo(ownerChat)-owner, h.wire.sendsTo(memberChat)-maria, h.wire.sendsTo(789)-petr)
	}
	digest := h.wire.snapshot()
	if !strings.Contains(digest.Text, "Вопросы на сегодня: 2") {
		t.Fatal(digest.Text)
	}
	soap := h.state("Мыло")
	soapPlan := h.plan(soap.Config.ID)
	h.pressIn(memberChat, "Паста")
	h.pressIn(memberChat, "Нет")
	h.pressIn(memberChat, "Пусто")
	if !reflect.DeepEqual(soap, h.state("Мыло")) || !reflect.DeepEqual(soapPlan, h.plan(soap.Config.ID)) {
		t.Fatal("one item's answer moved another item")
	}
	if notice := h.process(h.buttonOn(ownerChat, digest, "Паста")); notice != "Предмет уже изменён. Список обновлён." {
		t.Fatal("stale digest button", notice)
	}
	if text := h.wire.snapshot().Text; !strings.Contains(text, "Вопросы на сегодня: 1") || hasButton(h.wire.snapshot(), "Паста") || !hasButton(h.wire.snapshot(), "Мыло") {
		t.Fatal("refreshed digest", text)
	}
	h.command("/family")
	h.press("Выключить мои ежедневные вопросы")
	owner, maria, petr = h.wire.sendsTo(ownerChat), h.wire.sendsTo(memberChat), h.wire.sendsTo(789)
	// The overdue soap plan stays due; 21:00 summer time is no longer the slot.
	h.now = time.Date(2026, 10, 25, 19, 0, 0, 0, time.UTC)
	tick()
	if h.wire.sendsTo(memberChat) != maria {
		t.Fatal("digest sent at the old summer-time slot")
	}
	h.now = time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC)
	tick()
	if h.wire.sendsTo(ownerChat) != owner || h.wire.sendsTo(memberChat) != maria+1 || h.wire.sendsTo(789) != petr {
		t.Fatal("quiet settings ignored", h.wire.sendsTo(ownerChat)-owner, h.wire.sendsTo(memberChat)-maria, h.wire.sendsTo(789)-petr)
	}
	if !reflect.DeepEqual(soapPlan, h.plan(soap.Config.ID)) || !hasButton(h.wire.snapshotIn(memberChat), "Мыло") {
		t.Fatal("an overdue plan was moved or lost", soapPlan, h.plan(soap.Config.ID))
	}
}

func TestForgedCallbacksAnswerOnlyInTheirOwnChat(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.join(memberChat, "Мария")
	h.command("/items")
	forged := h.button("Паста")
	forged.Callback.From.ID = memberChat
	forged.Callback.Message.Chat.ID = memberChat
	if notice := h.process(forged); !strings.Contains(notice, "устарела") {
		t.Fatal("a member used the owner's screen", notice)
	}
	h.commandIn(memberChat, "/items")
	crossed := h.buttonIn(memberChat, "Паста")
	crossed.Callback.From.ID = ownerChat
	if notice := h.process(crossed); !strings.Contains(notice, "Нет доступа") {
		t.Fatal("a callback from another user was accepted", notice)
	}
}

func TestAgentMessagesStayWithTheOwner(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.join(memberChat, "Мария")
	sent := h.wire.sendsTo(memberChat)
	if err := h.service.NotifyOwner(testContext, "Корзина собрана", h.now); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.RequestChecks(testContext, []string{h.state("Паста").Config.ID}, h.now); err != nil {
		t.Fatal(err)
	}
	h.flush()
	// The two messages go out in no particular order; both are the owner's.
	notified, asked := false, false
	h.wire.mu.Lock()
	for key, msg := range h.wire.messages {
		notified = notified || key.chat == ownerChat && strings.Contains(msg.Text, "Корзина собрана")
		asked = asked || key.chat == ownerChat && hasButton(msg, "Нет")
	}
	h.wire.mu.Unlock()
	if h.wire.sendsTo(memberChat) != sent || !notified || !asked {
		t.Fatal("agent messages left the owner's chat", notified, asked)
	}
}
