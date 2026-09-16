package telegram

import (
	"context"
	"errors"
)

// ClearKeyboard removes only inline markup, preserving the message text. An
// explicit empty array is intentional; omitting reply_markup is not a removal.
func (c *Client) ClearKeyboard(ctx context.Context, chatID, messageID int64) error {
	if chatID == 0 || messageID <= 0 {
		return errors.New("chat and message ID required")
	}
	var result Message
	err := c.call(ctx, "editMessageReplyMarkup", struct {
		ChatID    int64    `json:"chat_id"`
		MessageID int64    `json:"message_id"`
		Keyboard  Keyboard `json:"reply_markup"`
	}{chatID, messageID, Keyboard{Rows: [][]Button{}}}, &result)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.NotModified {
		return nil
	}
	return err
}
