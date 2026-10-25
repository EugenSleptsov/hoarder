package bot

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/EugenSleptsov/hoarder/internal/dialog"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

// Family access: the owner invites household members with one-time links.
// Every member uses the same items from their own private chat. Who is in the
// family never touches item evidence, forecasts or question dates.

const (
	maxMembers = 10
	maxInvites = 5
	inviteTTL  = 24 * time.Hour
)

// member is a person the owner let in through an invitation.
type member struct {
	ChatID   int64
	Name     string
	JoinedAt time.Time
	// Invite is the code used to join; it binds a removal to this membership.
	Invite string
}

// invite is a one-time join code. It authorises nothing until it is used.
type invite struct {
	Code      string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedBy    int64 `json:",omitempty"`
	Revoked   bool  `json:",omitempty"`
}

func (in invite) open(now time.Time) bool {
	return in.UsedBy == 0 && !in.Revoked && now.Before(in.ExpiresAt)
}

// chatKey names per-person records: "owner" for the owner, else the chat ID.
func chatKey(chat int64) string {
	if chat == 0 {
		return "owner"
	}
	return strconv.FormatInt(chat, 10)
}

// SetUsername sets the bot's @username for invitation links. Call it before
// the service handles updates; without it an invitation shows a code to send.
func (s *Service) SetUsername(name string) { s.username = name }

// sender maps a private chat to the chat its screens use: zero for the owner,
// the chat ID for a current family member. In a private chat the user and the
// chat IDs agree; anything else, and a removed member, has no access.
func (s *Service) sender(ctx context.Context, tx *sqlstore.Tx, c telegram.Chat, user int64) (int64, bool, error) {
	if c.Type != "private" || c.ID <= 0 || c.ID != user {
		return 0, false, nil
	}
	if c.ID == s.db.Owner() {
		return 0, true, nil
	}
	var m member
	err := tx.Get(ctx, "member", chatKey(c.ID), &m)
	if errors.Is(err, sqlstore.ErrNotFound) {
		return 0, false, nil
	}
	return c.ID, err == nil, err
}

// message routes ordinary text. A foreign chat gets no reply and leaves no
// state, except when it opens an invitation link.
func (s *Service) message(ctx context.Context, tx *sqlstore.Tx, m telegram.Message, now time.Time) error {
	chat, allowed, err := s.sender(ctx, tx, m.Chat, m.Chat.ID)
	if err != nil {
		return err
	}
	if allowed {
		return s.command(ctx, tx, chat, m.Text, now)
	}
	code, found := strings.CutPrefix(strings.TrimSpace(m.Text), "/start ")
	if !found || m.Chat.Type != "private" || m.Chat.ID <= 0 {
		return nil
	}
	return s.join(ctx, tx, m.Chat, strings.TrimSpace(code), now)
}

// loadInvite reports malformed and unknown codes as missing.
func loadInvite(ctx context.Context, tx *sqlstore.Tx, code string) (invite, bool, error) {
	var in invite
	raw, err := hex.DecodeString(code)
	if err != nil || len(raw) != 12 || hex.EncodeToString(raw) != code {
		return in, false, nil
	}
	err = tx.Get(ctx, "invite", code, &in)
	if errors.Is(err, sqlstore.ErrNotFound) {
		return in, false, nil
	}
	return in, err == nil, err
}

func (s *Service) openInvites(ctx context.Context, tx *sqlstore.Tx, now time.Time) ([]invite, error) {
	rows, err := tx.List(ctx, "invite")
	if err != nil {
		return nil, err
	}
	open := []invite{}
	for _, raw := range rows {
		var in invite
		if err = json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		if in.open(now) {
			open = append(open, in)
		}
	}
	return open, nil
}

func (s *Service) members(ctx context.Context, tx *sqlstore.Tx) ([]member, error) {
	rows, err := tx.List(ctx, "member")
	if err != nil {
		return nil, err
	}
	members := []member{}
	for _, raw := range rows {
		var m member
		if err = json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	slices.SortStableFunc(members, func(a, b member) int { return a.JoinedAt.Compare(b.JoinedAt) })
	return members, nil
}

func quiet(ctx context.Context, tx *sqlstore.Tx, chat int64) (bool, error) {
	var q bool
	err := tx.Get(ctx, "quiet", chatKey(chat), &q)
	if errors.Is(err, sqlstore.ErrNotFound) {
		return false, nil
	}
	return q, err
}

// recipients lists the chats that get the daily digest: the owner and every
// member, except those who switched daily questions off for themselves.
func (s *Service) recipients(ctx context.Context, tx *sqlstore.Tx) ([]int64, error) {
	members, err := s.members(ctx, tx)
	if err != nil {
		return nil, err
	}
	chats := []int64{0}
	for _, m := range members {
		chats = append(chats, m.ChatID)
	}
	out := []int64{}
	for _, chat := range chats {
		q, err := quiet(ctx, tx, chat)
		if err != nil {
			return nil, err
		}
		if !q {
			out = append(out, chat)
		}
	}
	return out, nil
}

// displayName keeps a Telegram name short and printable for lists and buttons.
func displayName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(name, ""))
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 40 {
		name = strings.TrimSpace(string(r[:40]))
	}
	if name == "" {
		return "Участник"
	}
	return name
}

func (s *Service) localTime(t time.Time) string {
	loc, err := time.LoadLocation(s.cfg.Zone)
	if err != nil {
		return t.UTC().Format("02.01.2006 15:04") + " UTC"
	}
	return t.In(loc).Format("02.01.2006 15:04")
}

func (s *Service) slot() string {
	return fmt.Sprintf("%02d:%02d (%s)", s.cfg.Hour, s.cfg.Minute, s.cfg.Zone)
}

// join admits a new member with an open invitation. A code nobody issued gets
// no reply, so strangers cannot make the bot talk; a used or expired code gets
// a short explanation, since only someone given the link can know it.
func (s *Service) join(ctx context.Context, tx *sqlstore.Tx, c telegram.Chat, code string, now time.Time) error {
	in, found, err := loadInvite(ctx, tx, code)
	if err != nil || !found {
		return err
	}
	at := place{Chat: c.ID}
	if !in.open(now) {
		return s.note(ctx, tx, "Это приглашение уже использовано, отозвано или устарело. Попросите в семье новую ссылку.", nil, at, now)
	}
	members, err := s.members(ctx, tx)
	if err != nil {
		return err
	}
	if len(members) >= maxMembers {
		return s.note(ctx, tx, "В семье уже максимальное число участников. Попросите владельца освободить место.", nil, at, now)
	}
	m := member{ChatID: c.ID, Name: displayName(c.Name()), JoinedAt: now, Invite: code}
	if err = tx.Insert(ctx, "member", chatKey(c.ID), m); err != nil {
		return err
	}
	in.UsedBy = c.ID
	if err = tx.Put(ctx, "invite", code, in); err != nil {
		return err
	}
	welcome := "Вы подключены к домашнему списку запасов.\n\nБаза общая: предметы, ответы и список покупок видят и меняют все участники семьи. Ежедневные вопросы приходят в " + s.slot() + "; выключить их для себя можно в /family.\n\nКоманды: /items, /today, /shopping, /add, /manage, /family. Ответы — кнопками."
	if err = s.note(ctx, tx, welcome, []action{{Label: "Все предметы", Kind: "view", View: "all"}, {Label: "Семья", Kind: "family"}}, at, now); err != nil {
		return err
	}
	return s.note(ctx, tx, m.Name+" подключается к семье: доступны все предметы, ежедневные вопросы включены. Убрать доступ можно в /family.", []action{{Label: "Семья", Kind: "family"}}, place{}, now)
}

// knownInvite answers the owner or a member who opens an invitation link. It
// never uses up the invitation.
func (s *Service) knownInvite(ctx context.Context, tx *sqlstore.Tx, at place, code string, now time.Time) error {
	in, found, err := loadInvite(ctx, tx, code)
	if err != nil {
		return err
	}
	if !found {
		return s.menu(ctx, tx, "all", 0, nil, at, now)
	}
	text := "Вы уже подключены к семье."
	if at.Chat == 0 {
		text = "Это ссылка для члена семьи: перешлите её. Владельцу приглашение не нужно."
	}
	if in.open(now) {
		text += " Приглашение не израсходовано."
	}
	return s.note(ctx, tx, text, []action{{Label: "Все предметы", Kind: "view", View: "all"}, {Label: "Семья", Kind: "family"}}, at, now)
}

func (s *Service) familyScreen(ctx context.Context, tx *sqlstore.Tx, at place, lead string, now time.Time) error {
	members, err := s.members(ctx, tx)
	if err != nil {
		return err
	}
	invites, err := s.openInvites(ctx, tx, now)
	if err != nil {
		return err
	}
	status := func(chat int64) (string, bool, error) {
		q, err := quiet(ctx, tx, chat)
		if q {
			return "ежедневные вопросы выключены", q, err
		}
		return "ежедневные вопросы включены", q, err
	}
	var b strings.Builder
	if lead != "" {
		b.WriteString(lead + "\n\n")
	}
	b.WriteString("Семья\n\nБаза общая: все участники видят и меняют одни и те же предметы, каждый в своём чате с ботом. Ежедневные вопросы в " + s.slot() + " приходят каждому, кто их не выключил; ответ одного обновляет предмет для всех.\n")
	line, mine, err := status(0)
	if err != nil {
		return err
	}
	you := func(chat int64) string {
		if chat == at.Chat {
			return " (вы)"
		}
		return ""
	}
	b.WriteString("\nВладелец" + you(0) + " — " + line)
	for _, m := range members {
		line, q, err := status(m.ChatID)
		if err != nil {
			return err
		}
		if m.ChatID == at.Chat {
			mine = q
		}
		b.WriteString("\n• " + m.Name + you(m.ChatID) + ", с " + s.localTime(m.JoinedAt)[:10] + " — " + line)
	}
	owner := at.Chat == 0
	if owner && len(members) == 0 {
		b.WriteString("\nДругих участников пока нет.")
	}
	if owner && len(invites) > 0 {
		last := invites[0].ExpiresAt
		for _, in := range invites {
			if in.ExpiresAt.After(last) {
				last = in.ExpiresAt
			}
		}
		fmt.Fprintf(&b, "\nДействующих приглашений: %d, последнее — до %s.", len(invites), s.localTime(last))
	}
	actions := []action{}
	if owner {
		actions = append(actions, action{Label: "Пригласить", Kind: "invite"})
	}
	if mine {
		actions = append(actions, action{Label: "Включить мои ежедневные вопросы", Kind: "quiet", Field: "on"})
	} else {
		actions = append(actions, action{Label: "Выключить мои ежедневные вопросы", Kind: "quiet", Field: "off"})
	}
	if owner {
		for _, m := range members {
			actions = append(actions, action{Label: "Убрать: " + m.Name, Kind: "remove_preview", Member: m.ChatID, Field: m.Invite})
		}
		if len(invites) > 0 {
			actions = append(actions, action{Label: "Отозвать приглашения", Kind: "revoke"})
		}
	}
	actions = append(actions, action{Label: "Все предметы", Kind: "view", View: "all"})
	return s.note(ctx, tx, b.String(), actions, at, now)
}

func (s *Service) familyCallback(ctx context.Context, tx *sqlstore.Tx, a action, at place, now time.Time) (string, error) {
	switch a.Kind {
	case "family":
		return "", s.familyScreen(ctx, tx, at, "", now)
	case "quiet":
		// Each person switches only their own daily digest.
		if a.Field == "off" {
			if err := tx.Put(ctx, "quiet", chatKey(at.Chat), true); err != nil {
				return "", err
			}
			return "Сохранено.", s.familyScreen(ctx, tx, at, "Ежедневные вопросы для вас выключены. Вопросы на сегодня всегда есть в /today.", now)
		}
		if err := tx.Delete(ctx, "quiet", chatKey(at.Chat)); err != nil {
			return "", err
		}
		return "Сохранено.", s.familyScreen(ctx, tx, at, "Ежедневные вопросы для вас включены.", now)
	}
	if at.Chat != 0 {
		return "Это может только владелец.", s.familyScreen(ctx, tx, at, "", now)
	}
	switch a.Kind {
	case "invite":
		return s.invite(ctx, tx, at, now)
	case "revoke":
		invites, err := s.openInvites(ctx, tx, now)
		if err != nil {
			return "", err
		}
		for _, in := range invites {
			in.Revoked = true
			if err = tx.Put(ctx, "invite", in.Code, in); err != nil {
				return "", err
			}
		}
		return "Приглашения отозваны.", s.familyScreen(ctx, tx, at, "Неиспользованные приглашения отозваны: по ним больше нельзя подключиться.", now)
	case "remove_preview", "remove":
		var m member
		err := tx.Get(ctx, "member", chatKey(a.Member), &m)
		if err != nil && !errors.Is(err, sqlstore.ErrNotFound) {
			return "", err
		}
		// A re-joined member is a new membership; an old screen cannot remove it.
		if err != nil || a.Member == 0 || m.Invite != a.Field {
			return "Участник уже отключён.", s.familyScreen(ctx, tx, at, "", now)
		}
		if a.Kind == "remove_preview" {
			confirm := action{Label: "Да, убрать: " + m.Name, Kind: "remove", Member: m.ChatID, Field: m.Invite}
			text := m.Name + "\n\nУбрать из семьи? Доступ к боту закроется сразу: ежедневные вопросы прекратятся, старые кнопки перестанут работать. Предметы и ответы останутся в общей базе. Вернуть можно новым приглашением."
			return "", s.note(ctx, tx, text, []action{confirm, {Label: "Отмена", Kind: "family"}}, at, now)
		}
		if err = tx.Delete(ctx, "member", chatKey(m.ChatID)); err != nil {
			return "", err
		}
		if err = s.forgetChat(ctx, tx, m.ChatID, now); err != nil {
			return "", err
		}
		return "Участник убран.", s.familyScreen(ctx, tx, at, m.Name+" больше не имеет доступа к боту.", now)
	}
	return "Некорректная кнопка.", nil
}

// invite issues a one-time link. Only the owner reaches this.
func (s *Service) invite(ctx context.Context, tx *sqlstore.Tx, at place, now time.Time) (string, error) {
	members, err := s.members(ctx, tx)
	if err != nil {
		return "", err
	}
	if len(members) >= maxMembers {
		return "Мест нет.", s.familyScreen(ctx, tx, at, fmt.Sprintf("В семье уже %d участников — это предел. Чтобы пригласить ещё, сначала уберите кого-нибудь.", maxMembers), now)
	}
	open, err := s.openInvites(ctx, tx, now)
	if err != nil {
		return "", err
	}
	if len(open) >= maxInvites {
		return "Слишком много приглашений.", s.familyScreen(ctx, tx, at, fmt.Sprintf("Уже действует %d неиспользованных приглашений. Отзовите их или дождитесь, пока истекут.", maxInvites), now)
	}
	code, err := opaqueID()
	if err != nil {
		return "", err
	}
	in := invite{Code: code, CreatedAt: now, ExpiresAt: now.Add(inviteTTL)}
	if err = tx.Insert(ctx, "invite", code, in); err != nil {
		return "", err
	}
	how := "Перешлите эту ссылку члену семьи:\nhttps://t.me/" + s.username + "?start=" + code
	if s.username == "" {
		how = "Пусть член семьи откроет этого бота и отправит ему сообщение:\n/start " + code
	}
	text := "Приглашение в семью\n\n" + how + "\n\nПриглашение одноразовое и действует до " + s.localTime(in.ExpiresAt) + " (" + s.cfg.Zone + "). Кто его использует, получит доступ ко всем предметам и сможет их менять. Убрать доступ можно в /family."
	return "", s.note(ctx, tx, text, []action{{Label: "Назад к семье", Kind: "family"}}, at, now)
}

// forgetChat closes everything a removed member had open: their screens stop
// answering, live keyboards are cleared, and their open questions and text
// inputs end. Shared item data stays as it is.
func (s *Service) forgetChat(ctx context.Context, tx *sqlstore.Tx, chat int64, now time.Time) error {
	rows, err := tx.List(ctx, "screen")
	if err != nil {
		return err
	}
	for _, raw := range rows {
		var v screen
		if err = json.Unmarshal(raw, &v); err != nil {
			return err
		}
		if v.ChatID != chat || v.Used {
			continue
		}
		open := v.Dialog != nil && v.Dialog.Step != dialog.Done
		if open {
			var active string
			err = tx.Get(ctx, "active", v.Dialog.ItemID, &active)
			if err != nil && !errors.Is(err, sqlstore.ErrNotFound) {
				return err
			}
			if err == nil && active == v.ID {
				if err = tx.Delete(ctx, "active", v.Dialog.ItemID); err != nil {
					return err
				}
			}
		}
		if open || len(v.Actions) > 0 {
			err = s.retireScreen(ctx, tx, v, now)
		} else {
			// No buttons to clear: only stop the screen.
			v.Used = true
			err = tx.Put(ctx, "screen", v.ID, v)
		}
		if err != nil {
			return err
		}
	}
	for _, name := range []string{"adding", "awaiting_name", "renaming"} {
		if err = tx.Delete(ctx, "runtime", inputKey(name, chat)); err != nil {
			return err
		}
	}
	return tx.Delete(ctx, "quiet", chatKey(chat))
}
