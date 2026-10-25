package bot

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

func TestRenameKeepsEvidenceOverduePlanLinksAndOtherItem(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Нет", "25%")
	h.add("Бумага", true, "Одна", "Полная")
	before, other := h.state("Паста"), h.state("Бумага")
	plan, otherPlan := h.plan(before.Config.ID), h.plan(other.Config.ID)
	if _, err := h.service.SetShopLink(testContext, ShopLink{ItemID: before.Config.ID, Shop: ShopKnuspr, ProductID: "72415", ProductName: "elmex", UnitsPerPack: 1}, h.now); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(20 * 24 * time.Hour)
	if !plan.SendAt.Before(h.now) {
		t.Fatal("fixture plan is not overdue")
	}
	manage(h, "Паста")
	if row := h.wire.snapshot().Keyboard.Rows[1]; len(row) != 2 || row[0].Text != "Настройки" || row[1].Text != "Переименовать" {
		t.Fatalf("settings and rename should share a row: %v", row)
	}
	card := h.wire.snapshot().MessageID
	h.press("Переименовать")
	h.command("Зубная паста elmex")
	after := h.state("Зубная паста elmex")
	if after.Config.ID != before.Config.ID || after.Revision != before.Revision+1 {
		t.Fatal(after)
	}
	sameEvidence(t, before, after)
	wantConfig := before.Config
	wantConfig.Name = "Зубная паста elmex"
	if after.Config != wantConfig || after.Paused || after.Archived {
		t.Fatal("rename changed more than the name", after.Config)
	}
	p := h.plan(before.Config.ID)
	if p.ExpectedRevision != after.Revision {
		t.Fatal("plan not rebound to the renamed revision")
	}
	p.ExpectedRevision = plan.ExpectedRevision
	if !reflect.DeepEqual(p, plan) {
		t.Fatal("rename moved the overdue date", p, plan)
	}
	if msg := h.wire.message(card); !strings.HasPrefix(msg.Text, "Название изменено: «Паста» → «Зубная паста elmex»") {
		t.Fatal("rename result not shown in the prompt message", msg.Text)
	}
	need := h.needs(h.now.Add(24*time.Hour), false)["Зубная паста elmex"]
	if len(need.Links) != 1 || need.Links[0].ProductID != "72415" {
		t.Fatal("shop link lost", need)
	}
	if !reflect.DeepEqual(other, h.state("Бумага")) || !reflect.DeepEqual(otherPlan, h.plan(other.Config.ID)) {
		t.Fatal("rename touched another item")
	}
	h.reopen()
	if err := h.db.Transaction(testContext, func(tx *sqlstore.Tx) error {
		replayed, err := tx.Replay(testContext, after.Config.ID)
		if err == nil && !reflect.DeepEqual(replayed, after) {
			t.Fatal("rename replay mismatch")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h.command("ещё текст")
	if h.state("Зубная паста elmex").Revision != after.Revision {
		t.Fatal("a later message renamed again")
	}
}

func TestRenameRejectsTakenAndInvalidNamesUntilCancelled(t *testing.T) {
	h := newHarness(t)
	h.add("Мыло", false, "Нет", "50%")
	h.add("Бумага", true, "Одна", "Полная")
	h.add("Шампунь", false, "Нет", "50%")
	manage(h, "Шампунь")
	h.press("Убрать в архив")
	h.press("Подтверждаю архивирование")
	before := h.state("Мыло")
	manage(h, "Мыло")
	h.press("Переименовать")
	for _, input := range []string{"бумага", "ШАМПУНЬ", "Жидкое\nмыло", strings.Repeat("я", 81)} {
		h.command(input)
		if !reflect.DeepEqual(before, h.state("Мыло")) {
			t.Fatalf("%q renamed the item", input)
		}
		if text := h.wire.snapshot().Text; !strings.Contains(text, "/cancel") {
			t.Fatalf("%q: rename prompt not kept: %s", input, text)
		}
	}
	h.command("/cancel")
	if !strings.Contains(h.wire.snapshot().Text, "Переименование отменено") {
		t.Fatal(h.wire.snapshot().Text)
	}
	h.command("Жидкое мыло")
	if !reflect.DeepEqual(before, h.state("Мыло")) {
		t.Fatal("text after /cancel renamed the item")
	}
	// Only the case changes: the item does not collide with itself.
	manage(h, "Мыло")
	h.press("Переименовать")
	h.command("МЫЛО")
	if got := h.state("МЫЛО"); got.Config.ID != before.Config.ID {
		t.Fatal(got)
	}
}

func TestRenamePromptEndsOnTapExpiryNewerAnswerAndAdding(t *testing.T) {
	h := newHarness(t)
	h.add("Мыло", false, "Нет", "50%")
	before := h.state("Мыло")
	unchanged := func(why string) {
		t.Helper()
		if got := h.state("Мыло"); got.Config.Name != "Мыло" {
			t.Fatal(why, got.Config)
		}
	}

	manage(h, "Мыло")
	h.press("Переименовать")
	h.press("Отмена")
	h.command("Жидкое мыло")
	unchanged("prompt Cancel button did not end the rename")

	manage(h, "Мыло")
	h.press("Переименовать")
	h.now = h.now.Add(renameWindow)
	h.command("Жидкое мыло")
	unchanged("expired prompt renamed")
	if !strings.Contains(h.wire.snapshot().Text, "истекло") {
		t.Fatal(h.wire.snapshot().Text)
	}

	manage(h, "Мыло")
	h.press("Переименовать")
	h.command("/items")
	h.command("Жидкое мыло")
	unchanged("a command did not close the prompt")

	// A stock answer from an older question lands while the prompt is open.
	h.command("/items")
	h.press("Мыло")
	answer := h.button("Не знаю / не смотрел")
	manage(h, "Мыло")
	h.press("Переименовать")
	if notice := h.process(answer); notice != "Ответ принят." {
		t.Fatal(notice)
	}
	if h.state("Мыло").Revision == before.Revision {
		t.Fatal("fixture answer did not change the item")
	}
	h.command("Жидкое мыло")
	unchanged("stale revision renamed")
	if !strings.Contains(h.wire.snapshot().Text, "изменился") {
		t.Fatal(h.wire.snapshot().Text)
	}

	manage(h, "Мыло")
	h.press("Переименовать")
	h.press("Отмена")
	manage(h, "Мыло")
	h.press("Переименовать")
	h.command("/add")
	h.command("Жидкое мыло")
	unchanged("/add did not take over the text input")
	if !strings.HasPrefix(h.wire.snapshot().Text, "Жидкое мыло") {
		t.Fatal("typed name did not start adding", h.wire.snapshot().Text)
	}
}

func TestRenameRetiresOpenStockQuestion(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.command("/items")
	h.press("Паста")
	old := h.button("Не знаю / не смотрел")
	manage(h, "Паста")
	h.press("Переименовать")
	h.command("Зубная паста")
	renamed := h.state("Зубная паста")
	if notice := h.process(old); notice == "Ответ принят." {
		t.Fatal("old question accepted after rename")
	}
	if !reflect.DeepEqual(renamed, h.state("Зубная паста")) {
		t.Fatal("stale answer changed the renamed item")
	}
}

func TestRenameIsNotOfferedForArchivedItems(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	manage(h, "Паста")
	h.press("Убрать в архив")
	h.press("Подтверждаю архивирование")
	h.command("/archive")
	h.press("Паста")
	for _, row := range h.wire.snapshot().Keyboard.Rows {
		for _, b := range row {
			if b.Text == "Переименовать" {
				t.Fatal("archived item offers rename")
			}
		}
	}
}
