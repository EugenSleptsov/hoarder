// Package telegram is a transport adapter; it never updates inventory itself.
// API reference: https://core.telegram.org/bots/api
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	ErrTransport = errors.New("telegram transport failed")
	ErrProtocol  = errors.New("invalid Telegram response")
	tokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
	// usernamePattern is a Telegram bot username without the leading @.
	usernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)
)

type Client struct {
	token, base string
	http        *http.Client
}

func New(token string) (*Client, error) {
	return newClient(token, "https://api.telegram.org", &http.Client{Timeout: 60 * time.Second})
}

func newClient(token, base string, transport *http.Client) (*Client, error) {
	if !tokenPattern.MatchString(token) || transport == nil {
		return nil, errors.New("invalid bot token or HTTP client")
	}
	client := *transport
	// Never forward a token-bearing path to a redirect target.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{token, strings.TrimRight(base, "/"), &client}, nil
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
	// name is display-only. It stays unexported so marshalled updates, and
	// the receipt hashes stored for them before names existed, are unchanged.
	name string
}

// Name is the private chat's display name, empty when Telegram sent none.
func (c Chat) Name() string { return c.name }

func (c *Chat) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID        int64  `json:"id"`
		Type      string `json:"type"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	name := strings.TrimSpace(raw.FirstName + " " + raw.LastName)
	if name == "" && raw.Username != "" {
		name = "@" + raw.Username
	}
	*c = Chat{ID: raw.ID, Type: raw.Type, name: name}
	return nil
}

type User struct {
	ID int64 `json:"id"`
}
type Message struct {
	ID   int64  `json:"message_id"`
	Date int64  `json:"date"`
	Chat Chat   `json:"chat"`
	Text string `json:"text"`
}
type Callback struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}
type Update struct {
	ID       int64     `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}
type Keyboard struct {
	Rows [][]Button `json:"inline_keyboard"`
}
type Text struct {
	ChatID    int64     `json:"chat_id"`
	MessageID int64     `json:"message_id,omitempty"`
	Text      string    `json:"text"`
	Keyboard  *Keyboard `json:"reply_markup,omitempty"`
}

type APIError struct {
	Code              int
	RetryAfterSeconds int
	NotModified       bool
}

func (e *APIError) Error() string { return fmt.Sprintf("Telegram API error %d", e.Code) }

func (c *Client) Updates(ctx context.Context, offset int64) ([]Update, error) {
	if offset < 0 {
		return nil, errors.New("negative offsets may discard updates and are not supported")
	}
	// Bound the batch: 100 legal 4096-character Unicode messages can exceed
	// the response-size guard and otherwise poison the unchanged polling cursor.
	var result []Update
	err := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "limit": 20, "timeout": 30, "allowed_updates": []string{"message", "callback_query"}}, &result)
	return result, err
}

// Username asks Telegram for the bot's own @username; invitation links need it.
func (c *Client) Username(ctx context.Context) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	if err := c.call(ctx, "getMe", struct{}{}, &me); err != nil {
		return "", err
	}
	if !usernamePattern.MatchString(me.Username) {
		return "", ErrProtocol
	}
	return me.Username, nil
}

func (c *Client) Send(ctx context.Context, text Text) (Message, error) {
	if err := validateText(text); err != nil {
		return Message{}, err
	}
	if text.MessageID != 0 {
		return Message{}, errors.New("send must not contain an existing message ID")
	}
	var result Message
	err := c.call(ctx, "sendMessage", text, &result)
	if err == nil && result.ID == 0 {
		err = ErrProtocol
	}
	return result, err
}

func (c *Client) Edit(ctx context.Context, text Text) error {
	if err := validateText(text); err != nil {
		return err
	}
	if text.MessageID <= 0 {
		return errors.New("edit needs a message ID")
	}
	var result Message
	err := c.call(ctx, "editMessageText", text, &result)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.NotModified {
		return nil
	}
	return err
}

func (c *Client) Answer(ctx context.Context, callbackID string) error {
	if callbackID == "" {
		return errors.New("callback ID required")
	}
	var result bool
	return c.call(ctx, "answerCallbackQuery", map[string]string{"callback_query_id": callbackID}, &result)
}

func validateText(text Text) error {
	if text.ChatID == 0 || strings.TrimSpace(text.Text) == "" {
		return errors.New("chat and text required")
	}
	if text.Keyboard != nil {
		for _, row := range text.Keyboard.Rows {
			for _, button := range row {
				if button.Text == "" || len(button.Data) < 1 || len(button.Data) > 64 {
					return errors.New("callback button needs text and 1-64 data bytes")
				}
			}
		}
	}
	return nil
}

func (c *Client) call(ctx context.Context, method string, payload, result any) error {
	if c == nil || c.http == nil {
		return errors.New("uninitialised Telegram client")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("invalid Telegram request payload")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid Telegram request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// net/url errors embed the full token-bearing URL; never wrap them.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrTransport
	}
	defer resp.Body.Close()
	const maxBody = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return ErrProtocol
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Code        int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return ErrProtocol
	}
	if !envelope.OK || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := envelope.Code
		if code == 0 {
			code = resp.StatusCode
		}
		return &APIError{Code: code, RetryAfterSeconds: envelope.Parameters.RetryAfter,
			NotModified: code == 400 && strings.Contains(envelope.Description, "message is not modified")}
	}
	if len(envelope.Result) == 0 || bytes.Equal(envelope.Result, []byte("null")) || json.Unmarshal(envelope.Result, result) != nil {
		return ErrProtocol
	}
	return nil
}
