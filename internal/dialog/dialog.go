// Package dialog contains a persistent, transport-independent button state machine.
package dialog

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ErrStale = errors.New("button belongs to an older screen")
var idPattern = regexp.MustCompile(`^[a-f0-9]{24}$`)

type Step string

const (
	Reserve  Step = "reserve"
	Duration Step = "duration"
	Closed   Step = "closed"
	Level    Step = "level"
	History  Step = "history"
	NoUse    Step = "no_use"
	Done     Step = "done"
)

type Button struct{ Label, Action string }

// State is saved after EVERY transition, not only after the last answer.
// ID is a random opaque value supplied by the application (12 random bytes).
type State struct {
	Quick           bool      `json:"quick,omitempty"`
	DurationHint    bool      `json:"duration_hint,omitempty"`
	ID              string    `json:"id"`
	ItemID          string    `json:"item_id"`
	Name            string    `json:"name"`
	ItemRevision    uint64    `json:"item_revision"`
	Generation      uint64    `json:"generation"`
	Step            Step      `json:"step"`
	Creating        bool      `json:"creating"`
	WithReserve     bool      `json:"with_reserve"`
	DurationDays    float64   `json:"duration_days"`
	ClosedUnits     int       `json:"closed_units"`
	ClosedAt        time.Time `json:"closed_at"`
	Low             float64   `json:"low"`
	High            float64   `json:"high"`
	ObservedAt      time.Time `json:"observed_at"`
	HistorySince    time.Time `json:"history_since"`
	NoUseSince      time.Time `json:"no_use_since"`
	HistoryComplete bool      `json:"history_complete"`
	Unknown         bool      `json:"unknown"`
	Unused          bool      `json:"unused"`
	Cancelled       bool      `json:"cancelled"`
	// SetUnits and ReserveUnits are sizes chosen while adding; zero SetUnits
	// means one package with the yes/no reserve. Scale (full set plus reserve)
	// only sizes the count buttons and ClosedPage pages them; neither is evidence.
	SetUnits     int `json:"set_units,omitempty"`
	ReserveUnits int `json:"reserve_units,omitempty"`
	Scale        int `json:"scale,omitempty"`
	ClosedPage   int `json:"closed_page,omitempty"`
}

// MaxClosed bounds one closed-package count answer.
const MaxClosed = 99

// countRange is the page of closed counts shown: four worded ones for small
// items, a larger first page for big sets, then twelve per further page.
func (s State) countRange() (from, to int) {
	first := 4
	if s.Scale > 2 {
		first = min(24, ((s.Scale+2)/4+1)*4)
	}
	if s.ClosedPage > 0 {
		from = first + (s.ClosedPage-1)*12
		return from, min(MaxClosed, from+11)
	}
	return 0, first - 1
}

func (s State) countButtons() [][]Button {
	from, to := s.countRange()
	var rows [][]Button
	if from == 0 && to == 3 {
		rows = [][]Button{{{"Нет", "c0"}, {"Одна", "c1"}}, {{"Две", "c2"}, {"Три", "c3"}}}
	} else {
		for n := from; n <= to; n++ {
			if (n-from)%4 == 0 {
				rows = append(rows, nil)
			}
			rows[len(rows)-1] = append(rows[len(rows)-1], Button{strconv.Itoa(n), "c" + strconv.Itoa(n)})
		}
	}
	var paging []Button
	if s.ClosedPage > 0 {
		paging = append(paging, Button{fmt.Sprintf("Меньше %d", from), "cless"})
	}
	if to < MaxClosed {
		paging = append(paging, Button{fmt.Sprintf("Больше %d", to), "cmore"})
	}
	if paging != nil {
		rows = append(rows, paging)
	}
	return rows
}

// applyCount pages or records one closed-package count. ok is false for paging.
func (s State) applyCount(n State, action string, now time.Time) (State, bool) {
	switch action {
	case "cmore":
		n.ClosedPage++
		return n, false
	case "cless":
		n.ClosedPage--
		return n, false
	}
	n.ClosedUnits, _ = strconv.Atoi(action[1:])
	n.ClosedAt = now
	n.ClosedPage = 0
	return n, true
}

func New(id, itemID, name string, creating, reserve bool, revision uint64, history, since time.Time) (State, error) {
	if !idPattern.MatchString(id) || itemID == "" || strings.TrimSpace(name) == "" {
		return State{}, errors.New("invalid dialog identity")
	}
	step := Closed
	if creating {
		step = Reserve
	}
	return State{ID: id, ItemID: itemID, Name: name, Creating: creating, WithReserve: reserve, ItemRevision: revision, Step: step, HistorySince: history, NoUseSince: since}, nil
}

// Data contains no inventory quantity or ownership authority. Both the state
// generation and the allowed action must be checked against persisted state.
func (s State) Data(action string) string {
	return "h1:" + s.ID + ":" + strconv.FormatUint(s.Generation, 36) + ":" + action
}

func Parse(data string) (id string, generation uint64, action string, err error) {
	parts := strings.Split(data, ":")
	if len(data) > 64 || len(parts) != 4 || parts[0] != "h1" || !idPattern.MatchString(parts[1]) || len(parts[2]) == 0 || len(parts[3]) == 0 || len(parts[3]) > 8 {
		return "", 0, "", errors.New("invalid callback data")
	}
	n, e := strconv.ParseUint(parts[2], 36, 64)
	if e != nil || strconv.FormatUint(n, 36) != parts[2] {
		return "", 0, "", errors.New("invalid callback generation")
	}
	return parts[1], n, parts[3], nil
}

func (s State) Buttons() [][]Button {
	if s.Creating && s.Quick {
		return s.quickButtons()
	}
	var rows [][]Button
	switch s.Step {
	case Reserve:
		rows = [][]Button{{{"С резервом", "yes"}, {"Без резерва", "no"}}}
	case Duration:
		rows = [][]Button{{{"Около недели", "d7"}, {"Около месяца", "d30"}}, {{"Около 3 месяцев", "d90"}, {"Не знаю", "du"}}}
	case Closed:
		rows = s.countButtons()
	case Level:
		rows = [][]Button{{{"Полная", "p100"}, {"75%", "p75"}}, {{"50%", "p50"}, {"25%", "p25"}}, {{"Пусто", "p0"}}}
	case History:
		rows = [][]Button{{{"Не пополняли", "none"}}, {{"Пополняли / не помню", "maybe"}}}
	case NoUse:
		rows = [][]Button{{{"Подтверждаю", "confirm"}, {"Нет, проверить остаток", "back"}}}
	case Done:
		return nil
	default:
		return nil
	}
	if !s.Creating && (s.Step == Closed || s.Step == Level) && !s.NoUseSince.IsZero() {
		rows = append(rows, []Button{{"Не использовали", "unused"}})
	}
	if !s.Creating && s.Step != History && s.Step != NoUse {
		rows = append(rows, []Button{{"Не знаю / не смотрел", "skip"}})
	}
	rows = append(rows, []Button{{"Отмена", "cancel"}})
	return rows
}

func (s State) Text() string {
	if s.Creating && s.Quick {
		return s.quickText()
	}
	prefix := s.Name + "\n\n"
	switch s.Step {
	case Reserve:
		return prefix + "Поддерживать одну запасную упаковку? Это фиксированный резерв на ошибку прогноза."
	case Duration:
		return prefix + "На сколько обычно хватает одной стандартной упаковки? Это только начальное предположение."
	case Closed:
		return prefix + "Сколько сейчас закрытых стандартных упаковок? Открытую пока не считайте. Если их больше, чем на кнопках, нажмите «Больше»."
	case Level:
		return prefix + "Сколько сейчас осталось в открытой стандартной упаковке? Если открытой нет — «Пусто». Несколько открытых упаковок пока не поддерживаются."
	case History:
		return prefix + fmt.Sprintf("С предыдущей проверки (%s UTC) были пополнения? «Не пополняли» относится ко всему дому.", s.HistorySince.UTC().Format("02.01 15:04"))
	case NoUse:
		return prefix + fmt.Sprintf("С %s UTC никто дома не использовал этот предмет, не пополнял и не выбрасывал его?", s.NoUseSince.UTC().Format("02.01 15:04"))
	case Done:
		if s.Cancelled {
			return prefix + "Действие отменено. Остаток не изменён."
		}
		if s.Unknown {
			return prefix + "Остаток неизвестен. Расход по прогнозу продолжается."
		}
		return prefix + "Ответ сохранён."
	}
	return prefix + "Неизвестное состояние диалога."
}

// Apply only accepts an action that was actually offered on this generation.
// ObservedAt is captured at the stock answer, never at notification delivery.
func (s State) Apply(generation uint64, action string, now time.Time) (State, error) {
	if s.Step == Done || generation != s.Generation {
		return s, ErrStale
	}
	if now.IsZero() {
		return s, errors.New("answer time required")
	}
	allowed := false
	for _, row := range s.Buttons() {
		for _, button := range row {
			if button.Action == action {
				allowed = true
			}
		}
	}
	if !allowed {
		return s, errors.New("action is not allowed on this screen")
	}
	n := s
	n.Generation++
	if n.Generation == 0 {
		return s, errors.New("dialog generation overflow")
	}
	if action == "cancel" {
		n.Cancelled = true
		n.Step = Done
		return n, nil
	}
	if s.Creating && s.Quick {
		return s.applyQuick(n, action, now)
	}
	if action == "skip" {
		n.Unknown = true
		n.ObservedAt = now
		n.Step = Done
		return n, nil
	}
	if action == "unused" {
		n.Step = NoUse
		return n, nil
	}
	switch s.Step {
	case Reserve:
		n.WithReserve = action == "yes"
		n.Step = Duration
	case Duration:
		switch action {
		case "d7":
			n.DurationDays = 7
		case "d30":
			n.DurationDays = 30
		case "d90":
			n.DurationDays = 90
		case "du":
			n.DurationDays = 30
		}
		n.Step = Closed
	case Closed:
		var counted bool
		if n, counted = s.applyCount(n, action, now); counted {
			n.Step = Level
		}
	case Level:
		if s.ClosedAt.IsZero() || now.Before(s.ClosedAt) || now.Sub(s.ClosedAt) > 15*time.Minute {
			n.Step = Closed
			return n, nil
		}
		levels := map[string][2]float64{"p0": {0, 0}, "p25": {.125, .375}, "p50": {.375, .625}, "p75": {.625, .875}, "p100": {.875, 1}}
		v := levels[action]
		n.Low = v[0] + float64(n.ClosedUnits)
		n.High = v[1] + float64(n.ClosedUnits)
		n.ObservedAt = now
		n.Step = Done
		if !n.Creating && n.Low > 0 && !n.HistorySince.IsZero() && now.Sub(n.HistorySince) >= 24*time.Hour {
			n.Step = History
		}
	case History:
		// Do not combine an old stock check with a much later history answer.
		if now.Before(s.ObservedAt) || now.Sub(s.ObservedAt) > 15*time.Minute {
			n.ObservedAt = time.Time{}
			n.Step = Closed
			return n, nil
		}
		n.HistoryComplete = action == "none"
		n.Step = Done
	case NoUse:
		if action == "back" {
			n.Step = Closed
			break
		}
		n.Unused = true
		n.HistoryComplete = true
		n.ObservedAt = now
		n.Step = Done
	}
	return n, nil
}
