package bot

import (
	"reflect"
	"testing"
)

func TestItemsAreListedAlphabeticallyWithCompactNavigation(t *testing.T) {
	h := newHarness(t)
	h.add("Паста", true, "Одна", "Полная")
	h.add("бумага", false, "Нет", "50%")
	h.add("Ёршик", false, "Нет", "50%")
	h.add("Мыло", true, "Одна", "Полная")
	h.command("/items")
	names := map[string]bool{"Паста": true, "бумага": true, "Ёршик": true, "Мыло": true}
	var listed []string
	var nav [][]string
	for _, row := range h.wire.snapshot().Keyboard.Rows {
		labels := []string{}
		for _, b := range row {
			labels = append(labels, b.Text)
		}
		if len(row) == 1 && names[row[0].Text] {
			listed = append(listed, row[0].Text)
			continue
		}
		nav = append(nav, labels)
	}
	// Case and ё must not split the alphabet.
	if want := []string{"бумага", "Ёршик", "Мыло", "Паста"}; !reflect.DeepEqual(listed, want) {
		t.Fatalf("items %v, want %v", listed, want)
	}
	want := [][]string{{"Добавить предмет", "Управление предметами"}, {"Вопросы на сегодня", "Список покупок"}}
	if !reflect.DeepEqual(nav, want) {
		t.Fatalf("navigation rows %v, want %v", nav, want)
	}
	h.press("Управление предметами")
	if row := h.wire.snapshot().Keyboard.Rows[0]; len(row) != 1 || row[0].Text != "бумага" {
		t.Fatalf("manage list not alphabetical: %v", row)
	}
}
