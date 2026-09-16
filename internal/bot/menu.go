package bot

import (
	"context"
	"fmt"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/dialog"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

func (s *Service) menu(ctx context.Context, tx *sqlstore.Tx, view string, page int, ids []string, messageID int64, now time.Time) error {
	id, e := s.menuScreen(ctx, tx, view, page, ids, messageID, now)
	if e != nil {
		return e
	}
	return s.queue(ctx, tx, id, now, false, "")
}
func (s *Service) menuScreen(ctx context.Context, tx *sqlstore.Tx, view string, page int, ids []string, messageID int64, now time.Time) (string, error) {
	var e error
	if ids == nil {
		ids, e = s.selectItems(ctx, tx, view, now)
		if e != nil {
			return "", e
		}
	}
	// A persisted page is a candidate list, never authority over current lifecycle.
	ids, e = s.filterItems(ctx, tx, view, ids, now)
	if e != nil {
		return "", e
	}
	const pageSize = 8
	pages := (len(ids) + pageSize - 1) / pageSize
	if page < 0 || page >= pages {
		page = 0
	}
	title := "Предметы"
	if view == "manage" {
		title = "Управление предметами"
	}
	if view == "archive" {
		title = "Архив"
	}
	if view == "due" {
		title = "Вопросы на сегодня"
	}
	if view == "shop" {
		title = "Список покупок (рекомендации)"
	}
	text := fmt.Sprintf("%s: %d\nВыберите предмет. Ответы сохраняются после нажатия кнопок.", title, len(ids))
	actions := []action{}
	start := page * pageSize
	end := min(start+pageSize, len(ids))
	for _, id := range ids[start:end] {
		state, e := tx.LoadItem(ctx, id)
		if e != nil {
			return "", e
		}
		kind := "check"
		label := state.Config.Name
		if view == "shop" {
			kind = "bought"
			label = "Уже купили: " + label
		}
		if view == "manage" || view == "archive" || state.Paused {
			kind = "manage"
		}
		if state.Paused && !state.Archived {
			label += " (пауза)"
		}
		actions = append(actions, itemAction(label, kind, state))
	}
	if page > 0 {
		actions = append(actions, action{Label: "Назад", Kind: "page", View: view, Page: page - 1, IDs: ids})
	}
	if page+1 < pages {
		actions = append(actions, action{Label: "Дальше", Kind: "page", View: view, Page: page + 1, IDs: ids})
	}
	actions = append(actions, action{Label: "Добавить предмет", Kind: "add"})
	if view != "manage" {
		actions = append(actions, action{Label: "Управление предметами", Kind: "view", View: "manage"})
	}
	if view == "manage" {
		actions = append(actions, action{Label: "Архив", Kind: "view", View: "archive"})
	}
	if view != "due" {
		actions = append(actions, action{Label: "Вопросы на сегодня", Kind: "view", View: "due"})
	}
	if view != "shop" {
		actions = append(actions, action{Label: "Список покупок", Kind: "view", View: "shop"})
	}
	if view != "all" {
		actions = append(actions, action{Label: "Все предметы", Kind: "view", View: "all"})
	}
	id, e := opaqueID()
	if e != nil {
		return "", e
	}
	v := screen{MenuView: view, MenuIDs: ids, ID: id, Text: text, Actions: actions, MessageID: messageID, ExpiresAt: now.Add(7 * 24 * time.Hour)}
	return id, tx.Insert(ctx, "screen", id, v)
}

func render(v screen, owner int64) telegram.Text {
	text := v.Text
	rows := [][]telegram.Button{}
	if v.Dialog != nil && v.Dialog.Step != dialog.Done {
		text = v.Text + v.Dialog.Text()
		for _, row := range v.Dialog.Buttons() {
			buttons := []telegram.Button{}
			for _, b := range row {
				buttons = append(buttons, telegram.Button{Text: b.Label, Data: v.Dialog.Data(b.Action)})
			}
			rows = append(rows, buttons)
		}
	} else {
		for i, a := range v.Actions {
			rows = append(rows, []telegram.Button{{Text: a.Label, Data: fmt.Sprintf("m1:%s:%d", v.ID, i)}})
		}
	}
	return telegram.Text{ChatID: owner, MessageID: v.MessageID, Text: text, Keyboard: &telegram.Keyboard{Rows: rows}}
}
