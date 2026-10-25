package bot

import (
	"reflect"
	"testing"
)

func (h *harness) rows() [][]string {
	h.t.Helper()
	out := [][]string{}
	for _, row := range h.wire.snapshot().Keyboard.Rows {
		labels := []string{}
		for _, b := range row {
			labels = append(labels, b.Text)
		}
		out = append(out, labels)
	}
	return out
}

func TestHiddenSuggestionsPersistAndTypingStillWorks(t *testing.T) {
	h := newHarness(t)
	h.add("Зубная паста", true, "Одна", "Полная")
	h.command("/add")
	prompt := h.wire.snapshot().MessageID
	want := [][]string{{"Туалетная бумага"}, {"Шампунь"}, {"Мыло"}, {"Таблетки для посудомойки"}, {"Мусорные пакеты"}, {"Скрыть подсказки", "Отмена"}}
	if got := h.rows(); !reflect.DeepEqual(got, want) {
		t.Fatalf("add prompt %v, want %v", got, want)
	}
	h.press("Скрыть подсказки")
	stale := h.button("✕ Шампунь")
	h.press("✕ Шампунь")
	if notice := h.process(stale); notice == "Подсказка скрыта." {
		t.Fatal("old suggestion screen accepted a second tap")
	}
	h.press("✕ Мусорные пакеты")
	want = [][]string{{"✕ Туалетная бумага"}, {"✕ Мыло"}, {"✕ Таблетки для посудомойки"}, {"Вернуть скрытые", "Готово"}}
	if got := h.rows(); !reflect.DeepEqual(got, want) {
		t.Fatalf("suggestion screen %v, want %v", got, want)
	}
	h.press("Готово")
	if h.wire.snapshot().MessageID != prompt {
		t.Fatal("suggestion screens left the prompt message")
	}
	want = [][]string{{"Туалетная бумага"}, {"Мыло"}, {"Таблетки для посудомойки"}, {"Скрыть подсказки", "Отмена"}}
	if got := h.rows(); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompt after hiding %v, want %v", got, want)
	}
	h.reopen()
	h.command("/add")
	if got := h.rows(); !reflect.DeepEqual(got, want) {
		t.Fatalf("hidden suggestions returned after restart: %v", got)
	}
	h.command("Шампунь")
	h.press("Без резерва")
	h.press("Сейчас нет")
	h.press("Добавить")
	h.state("Шампунь")

	h.command("/add")
	h.press("Скрыть подсказки")
	h.press("Вернуть скрытые")
	h.press("Готово")
	// Restored, except a name that has become an item.
	want = [][]string{{"Туалетная бумага"}, {"Мыло"}, {"Таблетки для посудомойки"}, {"Мусорные пакеты"}, {"Скрыть подсказки", "Отмена"}}
	if got := h.rows(); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored prompt %v, want %v", got, want)
	}
}

func TestSuggestionScreenStaysInsideTheAddingFlow(t *testing.T) {
	h := newHarness(t)
	h.command("/add")
	h.press("Скрыть подсказки")
	for _, name := range commonNames {
		h.press("✕ " + name)
	}
	if got := h.rows(); !reflect.DeepEqual(got, [][]string{{"Вернуть скрытые", "Готово"}}) {
		t.Fatalf("all hidden %v", got)
	}
	h.press("Готово")
	if got := h.rows(); !reflect.DeepEqual(got, [][]string{{"Вернуть подсказки", "Отмена"}}) {
		t.Fatalf("prompt with every suggestion hidden %v", got)
	}
	h.press("Вернуть подсказки")
	if got := h.rows(); len(got) != len(commonNames)+1 || !reflect.DeepEqual(got[len(got)-1], []string{"Скрыть подсказки", "Отмена"}) {
		t.Fatalf("prompt after restoring %v", got)
	}
	h.press("Скрыть подсказки")
	done := h.button("Готово")
	h.command("Зубная нить")
	if notice := h.process(done); notice == "" {
		t.Fatal("suggestion screen stayed live after a typed name")
	}
	h.press("Без резерва")
	h.press("Сейчас нет")
	h.press("Добавить")
	h.state("Зубная нить")

	h.command("/add")
	h.press("Скрыть подсказки")
	h.command("/cancel")
	h.command("Мыло")
	if h.itemCount() != 1 {
		t.Fatal("/cancel on the suggestion screen did not end adding")
	}
}
