package dialog

import (
	"fmt"
	"strconv"
	"time"
)

const (
	Initial Step = "initial_stock"
	Review  Step = "review_item"
)

// NewQuick is opt-in so already persisted legacy wizards remain readable.
// The default duration is a disclosed cold-start assumption, not a learned rate.
func NewQuick(id, name string) (State, error) {
	s, err := New(id, id, name, true, false, 0, time.Time{}, time.Time{})
	s.Quick = true
	s.DurationDays = 30
	return s, err
}

func (s State) quickButtons() [][]Button {
	var rows [][]Button
	switch s.Step {
	case Reserve:
		rows = [][]Button{{{"С резервом", "yes"}, {"Без резерва", "no"}}}
	case Initial:
		rows = [][]Button{{{"Одна полная упаковка", "q1"}}, {{"Полная + одна запасная", "q2"}}, {{"Осталось примерно 50%", "qhalf"}}, {{"Сейчас нет", "q0"}, {"Указать остаток", "detail"}}}
	case Closed:
		rows = [][]Button{{{"Нет", "c0"}, {"Одна", "c1"}}, {{"Две", "c2"}, {"Три", "c3"}}}
	case Level:
		rows = [][]Button{{{"Полная", "p100"}, {"75%", "p75"}}, {{"50%", "p50"}, {"25%", "p25"}}, {{"Пусто", "p0"}}}
	case Duration:
		rows = [][]Button{{{"Около недели", "d7"}, {"Около месяца", "d30"}}, {{"Около 3 месяцев", "d90"}, {"Пока не знаю", "du"}}}
	case Review:
		rows = [][]Button{{{"Добавить", "save"}}, {{"Изменить остаток", "stock"}, {"Уточнить срок расхода", "duration"}}}
	case Done:
		return nil
	}
	if s.Step != Reserve {
		rows = append(rows, []Button{{"Назад", "back"}})
	}
	return append(rows, []Button{{"Отмена", "cancel"}})
}

func (s State) quickText() string {
	prefix := s.Name + "\n\n"
	switch s.Step {
	case Reserve:
		return prefix + "1/3 · Нужен фиксированный резерв в одну упаковку? Это настройка, не подтверждение наличия."
	case Initial:
		return prefix + "2/3 · Сколько сейчас дома? Выберите готовый вариант или уточните остаток. Считаем одинаковые стандартные упаковки."
	case Closed:
		return prefix + "Сколько закрытых упаковок? Открытую пока не считайте. Поддерживается от нуля до трёх."
	case Level:
		return prefix + "Сколько осталось в одной открытой упаковке? Если открытой нет — «Пусто»."
	case Duration:
		return prefix + "На сколько обычно хватает одной упаковки? Можно не уточнять: первоначальный ориентир — 30 дней, затем бот учится по ответам."
	case Review:
		reserve := "без резерва"
		if s.WithReserve {
			reserve = "с резервом: 1 упаковка"
		}
		duration := "Расход пока неизвестен. Начальный ориентир — 30 дней на упаковку."
		if s.DurationHint {
			duration = fmt.Sprintf("Ваш ориентир: %g дней на упаковку.", s.DurationDays)
		}
		return prefix + fmt.Sprintf("3/3 · Проверьте перед добавлением\nПолитика: %s\nОстаток: примерно %g–%g уп.\n%s\nРезерв автоматически не увеличивается.", reserve, s.Low, s.High, duration)
	case Done:
		if s.Cancelled {
			return prefix + "Добавление отменено."
		}
		return prefix + "Предмет добавлен. Ответ сохранён."
	}
	return prefix + "Неизвестный шаг добавления."
}

func (s State) applyQuick(n State, a string, now time.Time) (State, error) {
	if a == "back" {
		switch s.Step {
		case Initial:
			n.Step = Reserve
		case Closed:
			n.Step = Initial
		case Level:
			n.Step = Closed
		case Duration:
			n.Step = Review
		case Review:
			n.Step = Initial
		}
		return n, nil
	}
	switch s.Step {
	case Reserve:
		n.WithReserve = a == "yes"
		n.Step = Initial
	case Initial:
		if a == "detail" {
			n.Step = Closed
			break
		}
		levels := map[string][2]float64{"q1": {.875, 1}, "q2": {1.875, 2}, "qhalf": {.375, .625}, "q0": {0, 0}}
		v := levels[a]
		n.Low, n.High = v[0], v[1]
		n.ObservedAt = now
		n.Step = Review
	case Closed:
		n.ClosedUnits, _ = strconv.Atoi(a[1:])
		n.ClosedAt = now
		n.Step = Level
	case Level:
		if s.ClosedAt.IsZero() || now.Before(s.ClosedAt) || now.Sub(s.ClosedAt) > 15*time.Minute {
			n.Step = Closed
			break
		}
		levels := map[string][2]float64{"p0": {0, 0}, "p25": {.125, .375}, "p50": {.375, .625}, "p75": {.625, .875}, "p100": {.875, 1}}
		v := levels[a]
		n.Low = v[0] + float64(n.ClosedUnits)
		n.High = v[1] + float64(n.ClosedUnits)
		n.ObservedAt = now
		n.Step = Review
	case Duration:
		n.DurationHint = a != "du"
		n.DurationDays = 30
		if a == "d7" {
			n.DurationDays = 7
		}
		if a == "d90" {
			n.DurationDays = 90
		}
		n.Step = Review
	case Review:
		switch a {
		case "stock":
			n.Step = Initial
		case "duration":
			n.Step = Duration
		case "save":
			if s.ObservedAt.IsZero() || now.Before(s.ObservedAt) || now.Sub(s.ObservedAt) > 15*time.Minute {
				n.Step = Initial
				break
			}
			n.Step = Done
		}
	}
	return n, nil
}
