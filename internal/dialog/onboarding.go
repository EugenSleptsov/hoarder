package dialog

import (
	"fmt"
	"strconv"
	"time"
)

const (
	Initial     Step = "initial_stock"
	Review      Step = "review_item"
	SetSize     Step = "set_size"
	ReserveSize Step = "reserve_size"
)

// Sizes offered for a full set and a reserve of many packages.
var policySizes = []int{1, 2, 3, 4, 5, 6, 8, 10, 12, 16, 20, 24}

func sizeRows(prefix string, sizes []int) [][]Button {
	var rows [][]Button
	for i, n := range sizes {
		if i%4 == 0 {
			rows = append(rows, nil)
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], Button{strconv.Itoa(n), prefix + strconv.Itoa(n)})
	}
	return rows
}

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
		rows = [][]Button{{{"С резервом", "yes"}, {"Без резерва", "no"}}, {{"Много упаковок: набор и резерв", "many"}}}
	case SetSize:
		rows = sizeRows("f", policySizes[1:])
	case ReserveSize:
		rows = append([][]Button{{{"Без резерва", "r0"}}}, sizeRows("r", policySizes)...)
	case Initial:
		if s.SetUnits > 1 {
			rows = [][]Button{{{fmt.Sprintf("Полный набор: %d уп.", s.SetUnits), "q1"}}}
			if s.ReserveUnits > 0 {
				rows = append(rows, []Button{{fmt.Sprintf("Набор + резерв: %d уп.", s.SetUnits+s.ReserveUnits), "q2"}})
			}
			rows = append(rows, []Button{{"Примерно половина набора", "qhalf"}}, []Button{{"Сейчас нет", "q0"}, {"Указать остаток", "detail"}})
			break
		}
		rows = [][]Button{{{"Одна полная упаковка", "q1"}}, {{"Полная + одна запасная", "q2"}}, {{"Осталось примерно 50%", "qhalf"}}, {{"Сейчас нет", "q0"}, {"Указать остаток", "detail"}}}
	case Closed:
		rows = s.countButtons()
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
		return prefix + "1/3 · Нужен фиксированный резерв в одну упаковку? Это настройка, не подтверждение наличия. Если в работе нужно несколько упаковок — «Много упаковок»."
	case SetSize:
		return prefix + "1/3 · Полный набор: сколько упаковок должно быть в работе после покупки? Резерв — следующим шагом."
	case ReserveSize:
		return prefix + fmt.Sprintf("1/3 · Резерв сверх набора из %d уп.: сколько упаковок не трогать на случай ошибки прогноза? Когда остаток дойдёт до резерва, бот предложит докупить до набора плюс резерв.", s.SetUnits)
	case Initial:
		return prefix + "2/3 · Сколько сейчас дома? Выберите готовый вариант или уточните остаток. Считаем одинаковые стандартные упаковки."
	case Closed:
		return prefix + "Сколько закрытых упаковок? Открытую пока не считайте. Если их больше, чем на кнопках, нажмите «Больше»."
	case Level:
		return prefix + "Сколько осталось в одной открытой упаковке? Если открытой нет — «Пусто»."
	case Duration:
		return prefix + "На сколько обычно хватает одной упаковки? Можно не уточнять: первоначальный ориентир — 30 дней, затем бот учится по ответам."
	case Review:
		reserve := "без резерва"
		if s.WithReserve {
			reserve = "с резервом: 1 упаковка"
		}
		if s.SetUnits > 1 {
			reserve = fmt.Sprintf("полный набор %d уп., без резерва", s.SetUnits)
			if s.ReserveUnits > 0 {
				reserve = fmt.Sprintf("полный набор %d уп. + резерв %d уп.", s.SetUnits, s.ReserveUnits)
			}
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
		case SetSize:
			n.Step = Reserve
		case ReserveSize:
			n.Step = SetSize
		case Initial:
			n.Step = Reserve
			if s.SetUnits > 1 {
				n.Step = ReserveSize
			}
		case Closed:
			n.Step, n.ClosedPage = Initial, 0
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
		n.SetUnits, n.ReserveUnits, n.Scale = 0, 0, 0
		if a == "many" {
			n.Step = SetSize
			break
		}
		n.WithReserve = a == "yes"
		n.Step = Initial
	case SetSize:
		n.SetUnits, _ = strconv.Atoi(a[1:])
		n.Step = ReserveSize
	case ReserveSize:
		n.ReserveUnits, _ = strconv.Atoi(a[1:])
		n.WithReserve, n.Scale = n.ReserveUnits > 0, n.SetUnits+n.ReserveUnits
		n.Step = Initial
	case Initial:
		if a == "detail" {
			n.Step = Closed
			break
		}
		levels := map[string][2]float64{"q1": {.875, 1}, "q2": {1.875, 2}, "qhalf": {.375, .625}, "q0": {0, 0}}
		v := levels[a]
		if s.SetUnits > 1 {
			// The same shapes for a set: up to an eighth of a package open.
			set, all := float64(s.SetUnits), float64(s.SetUnits+s.ReserveUnits)
			v = map[string][2]float64{"q1": {set - .125, set}, "q2": {all - .125, all}, "qhalf": {set * .375, set * .625}, "q0": {0, 0}}[a]
		}
		n.Low, n.High = v[0], v[1]
		n.ObservedAt = now
		n.Step = Review
	case Closed:
		var counted bool
		if n, counted = s.applyCount(n, a, now); counted {
			n.Step = Level
		}
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
