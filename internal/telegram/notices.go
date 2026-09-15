package telegram

import (
	"context"
	"errors"
	"net/http"
	"unicode/utf8"
)

// NewWithClient keeps the official API origin while allowing a controlled HTTP
// transport for integration tests. New remains the production default.
func NewWithClient(token string, client *http.Client) (*Client, error) {
	return newClient(token, "https://api.telegram.org", client)
}

// AnswerNotice must also be used for stale/rejected callbacks to dismiss the
// Telegram client's progress indicator. It does not change domain state.
func (c *Client) AnswerNotice(ctx context.Context, callbackID, text string) error {
	if callbackID == "" || utf8.RuneCountInString(text) > 200 {
		return errors.New("invalid callback acknowledgement")
	}
	var result bool
	err := c.call(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
		"cache_time":        0,
	}, &result)
	if err == nil && !result {
		return ErrProtocol
	}
	return err
}
