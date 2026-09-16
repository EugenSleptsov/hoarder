package bot

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/app"
	"github.com/EugenSleptsov/hoarder/internal/forecast"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

func itemAction(label, kind string, state item.State) action {
	return action{Label: label, Kind: kind, ItemID: state.Config.ID, Bound: true, ExpectedRevision: state.Revision}
}

func configurationText(c item.Config) string {
	return fmt.Sprintf("Резерв: %g уп.\nВремя на покупку: %g дн.\nМаксимальный интервал проверки: %g дн.", c.ReserveUnits, c.LeadDays, c.MaxCheckDays)
}

func (s *Service) managementCard(ctx context.Context, tx *sqlstore.Tx, state item.State, messageID int64, now time.Time) error {
	status := "Опросы включены"
	if state.Paused {
		status = "Пауза: расход по прогнозу продолжается"
	}
	if state.Archived {
		status = "В архиве: история сохранена, опросы выключены"
	}
	text := state.Config.Name + "\n\n" + status + "\n" + configurationText(state.Config)
	var plan app.QuestionPlan
	if err := tx.Get(ctx, "plan", state.Config.ID, &plan); err != nil {
		return err
	}
	text += "\nСохранённая дата проверки: " + plan.LocalDate
	if !plan.SendAt.After(now) {
		text += " (уже наступила)"
	}
	text += "\nИзменение настроек не является измерением или покупкой."
	var actions []action
	control := func(label string, kind item.Kind) action {
		a := itemAction(label, "control", state)
		a.Control = kind
		return a
	}
	if state.Archived {
		actions = append(actions, control("Восстановить на паузе", item.Restore))
	} else {
		actions = append(actions, itemAction("Проверить остаток", "check", state), itemAction("Настройки", "settings", state))
		if state.Paused {
			actions = append(actions, control("Возобновить опросы", item.Resume))
		} else {
			actions = append(actions, control("Приостановить опросы", item.Pause))
		}
		actions = append(actions, itemAction("Убрать в архив", "archive_preview", state))
	}
	actions = append(actions, action{Label: "Все предметы", Kind: "view", View: "all"}, action{Label: "Архив", Kind: "view", View: "archive"})
	return s.note(ctx, tx, text, actions, messageID, now)
}

func (s *Service) managementCallback(ctx context.Context, tx *sqlstore.Tx, a action, commandID string, messageID int64, now time.Time) (string, error) {
	state, err := tx.LoadItem(ctx, a.ItemID)
	if errors.Is(err, sqlstore.ErrNotFound) {
		return "Предмет недоступен. Откройте /items.", nil
	}
	if err != nil {
		return "", err
	}
	if !a.Bound || state.Revision != a.ExpectedRevision {
		return "Предмет изменён. Откройте /manage.", nil
	}
	if a.Kind == "manage" {
		return "", s.managementCard(ctx, tx, state, messageID, now)
	}
	if state.Archived && !(a.Kind == "control" && a.Control == item.Restore) {
		return "Предмет в архиве. Откройте /archive.", nil
	}
	switch a.Kind {
	case "settings":
		actions := []action{}
		for _, choice := range []struct{ label, field string }{{"Резерв", "reserve"}, {"Время на покупку", "lead"}, {"Интервал проверки", "gap"}} {
			b := itemAction(choice.label, "setting", state)
			b.Field = choice.field
			actions = append(actions, b)
		}
		actions = append(actions, itemAction("Назад к предмету", "manage", state))
		return "", s.note(ctx, tx, state.Config.Name+"\n\n"+configurationText(state.Config), actions, messageID, now)
	case "setting":
		return "", s.settingChoices(ctx, tx, state, a.Field, messageID, now)
	case "config_preview":
		if a.Configuration == nil || a.Configuration.ID != state.Config.ID || a.Configuration.Validate() != nil {
			return "Некорректные настройки.", nil
		}
		confirm := itemAction("Сохранить настройки", "control", state)
		confirm.Control, confirm.Configuration = item.Reconfigure, a.Configuration
		text := state.Config.Name + "\n\nБыло:\n" + configurationText(state.Config) + "\n\nБудет:\n" + configurationText(*a.Configuration) + "\n\nФизический запас не изменится. Пересчитается только план этого предмета."
		return "", s.note(ctx, tx, text, []action{confirm, itemAction("Отмена", "manage", state)}, messageID, now)
	case "archive_preview":
		confirm := itemAction("Подтверждаю архивирование", "control", state)
		confirm.Control = item.Archive
		return "", s.note(ctx, tx, state.Config.Name+"\n\nУбрать предмет из реестра и опросов? История останется в базе. Восстановление доступно через /archive. Это не безвозвратное удаление данных.", []action{confirm, itemAction("Отмена", "manage", state)}, messageID, now)
	case "control":
		return s.applyControl(ctx, tx, state, a, commandID, messageID, now)
	}
	return "Некорректная кнопка.", nil
}

func (s *Service) settingChoices(ctx context.Context, tx *sqlstore.Tx, state item.State, field string, messageID int64, now time.Time) error {
	var values []float64
	switch field {
	case "reserve":
		values = []float64{0, 1}
	case "lead":
		values = []float64{1, 3, 7, 14}
	case "gap":
		values = []float64{7, 30, 90, 180}
	default:
		return errors.New("invalid setting field")
	}
	actions := []action{}
	for _, value := range values {
		cfg := state.Config
		label := fmt.Sprintf("%g дней", value)
		switch field {
		case "reserve":
			cfg.ReserveUnits, cfg.WithReserve = value, value > 0
			label = "Без резерва"
			if value > 0 {
				label = "Резерв: 1 упаковка"
			}
		case "lead":
			cfg.LeadDays = value
		case "gap":
			cfg.MaxCheckDays = value
		}
		a := itemAction(label, "config_preview", state)
		a.Configuration = &cfg
		actions = append(actions, a)
	}
	actions = append(actions, itemAction("Отмена", "manage", state))
	return s.note(ctx, tx, state.Config.Name+"\n\nВыберите новое значение. Следующий экран запросит подтверждение.", actions, messageID, now)
}

func (s *Service) applyControl(ctx context.Context, tx *sqlstore.Tx, state item.State, a action, commandID string, messageID int64, now time.Time) (string, error) {
	switch a.Control {
	case item.Pause, item.Resume, item.Archive, item.Restore, item.Reconfigure:
	default:
		return "Недопустимое действие.", nil
	}
	event := item.Event{ID: "control:" + commandID, ItemID: state.Config.ID, Kind: a.Control, At: now, Configuration: a.Configuration}
	// Validate before storage so a bad control becomes a durable rejected callback.
	if _, err := state.Apply(event); err != nil {
		return "Действие больше недоступно. Откройте /manage.", nil
	}
	next, err := tx.ApplyEvent(ctx, event, state.Revision)
	if err != nil {
		return "", err
	}
	var plan app.QuestionPlan
	if a.Control == item.Reconfigure {
		plan, err = app.PlanItem(next, forecast.Baseline{}, s.daily, now.Add(time.Second))
	} else {
		// Pause/resume/archive/restore retain the original, possibly overdue date.
		err = tx.Get(ctx, "plan", state.Config.ID, &plan)
		if err == nil && plan.ExpectedRevision != state.Revision {
			return "", errors.New("control found a stale plan")
		}
		plan.ExpectedRevision = next.Revision
	}
	if err != nil {
		return "", err
	}
	if err = tx.SaveQuestion(ctx, plan); err != nil {
		return "", err
	}
	if err = s.invalidateQuestion(ctx, tx, state.Config.ID); err != nil {
		return "", err
	}
	return "Изменение сохранено.", s.managementCard(ctx, tx, next, messageID, now)
}

func (s *Service) invalidateQuestion(ctx context.Context, tx *sqlstore.Tx, id string) error {
	var active string
	err := tx.Get(ctx, "active", id, &active)
	if errors.Is(err, sqlstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var v screen
	if err = tx.Get(ctx, "screen", active, &v); err != nil {
		return err
	}
	v.Used = true
	if err = tx.Put(ctx, "screen", active, v); err != nil {
		return err
	}
	if err = tx.Delete(ctx, "delivery", active); err != nil {
		return err
	}
	return tx.Delete(ctx, "active", id)
}

// filterItems only filters presentation candidates against their own current state.
func (s *Service) filterItems(ctx context.Context, tx *sqlstore.Tx, view string, ids []string, now time.Time) ([]string, error) {
	eligible, err := s.selectItems(ctx, tx, view, now)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(eligible))
	for _, id := range eligible {
		allowed[id] = true
	}
	out := []string{}
	for _, id := range ids {
		if allowed[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// refreshPending never rebinds an old menu ID to different actions. If a menu
// changes before delivery, retire it and queue a new opaque screen instead.
func (s *Service) refreshPending(ctx context.Context, tx *sqlstore.Tx, v *screen, job delivery, now time.Time) (bool, error) {
	if v.Dialog != nil && !v.Dialog.Creating {
		current, err := tx.LoadItem(ctx, v.Dialog.ItemID)
		if err != nil {
			return false, err
		}
		if current.Archived || current.Revision != v.Dialog.ItemRevision && v.Dialog.Step != "done" {
			v.Used = true
			return true, tx.Put(ctx, "screen", v.ID, v)
		}
	}
	if !job.Proactive {
		return false, nil
	}
	ids := v.MenuIDs
	if v.MenuView == "" {
		// Compatibility with queued schema-v1 digests created before menu metadata.
		var old session
		if err := tx.Get(ctx, "session", job.Date, &old); err != nil {
			return false, err
		}
		ids = old.ItemIDs
	}
	current, err := s.filterItems(ctx, tx, "due", ids, now)
	if err != nil {
		return false, err
	}
	changed := !reflect.DeepEqual(ids, current)
	for _, a := range v.Actions {
		if !a.Bound {
			continue
		}
		st, err := tx.LoadItem(ctx, a.ItemID)
		if err != nil {
			return false, err
		}
		if st.Revision != a.ExpectedRevision {
			changed = true
		}
	}
	if len(current) > 0 && !changed {
		return false, nil
	}
	v.Used = true
	if err = tx.Put(ctx, "screen", v.ID, v); err != nil {
		return false, err
	}
	if len(current) == 0 {
		return true, nil
	}
	id, err := s.menuScreen(ctx, tx, "due", 0, current, v.MessageID, now)
	if err != nil {
		return false, err
	}
	return true, s.queue(ctx, tx, id, now, true, job.Date)
}
