package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestClearKeyboardSendsExplicitEmptyArrayWithoutText(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+testToken+"/editMessageReplyMarkup" {
			t.Error(r.URL.Path)
		}
		var v map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Fatal(err)
		}
		if string(v["chat_id"]) != "42" || string(v["message_id"]) != "7" || string(v["reply_markup"]) != `{"inline_keyboard":[]}` {
			t.Error(v)
		}
		if _, present := v["text"]; present {
			t.Error("clearing markup changed text")
		}
		io.WriteString(w, `{"ok":true,"result":{"message_id":7,"chat":{"id":42}}}`)
	})
	if err := c.ClearKeyboard(context.Background(), 42, 7); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][2]int64{{0, 7}, {42, 0}, {42, -1}} {
		if err := c.ClearKeyboard(context.Background(), ids[0], ids[1]); err == nil {
			t.Fatal(ids)
		}
	}
}

func TestClearKeyboardIdempotenceAndErrors(t *testing.T) {
	for _, code := range []int{400, 403, 429, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				description := "failed"
				if code == 400 {
					description = "Bad Request: message is not modified"
				}
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": code, "description": description, "parameters": map[string]int{"retry_after": 12}})
			})
			err := c.ClearKeyboard(context.Background(), 42, 7)
			if code == 400 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != code || apiErr.RetryAfterSeconds != 12 {
				t.Fatal(err)
			}
		})
	}
}
