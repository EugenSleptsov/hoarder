package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfig(t *testing.T) {
	values := map[string]string{"HOARDER_CHAT_ID": "123", "TELEGRAM_BOT_TOKEN": "123:TEST"}
	get := func(k string) string { return values[k] }
	c, e := loadConfig(get, true)
	if e != nil || c.Schedule.Zone != "Europe/Berlin" || c.Schedule.Hour != 21 || c.Owner != 123 {
		t.Fatal(c, e)
	}
	for key, value := range map[string]string{"HOARDER_CHAT_ID": "-1", "HOARDER_POLL_TIME": "25:00", "HOARDER_TIMEZONE": "Invalid/Zone", "HOARDER_CATCH_UP": "0s"} {
		old := values[key]
		values[key] = value
		if _, e = loadConfig(get, true); e == nil {
			t.Fatal(key)
		}
		values[key] = old
	}
}
func TestTokenFileAndBackupConfiguration(t *testing.T) {
	values := map[string]string{"HOARDER_CHAT_ID": "123"}
	get := func(k string) string { return values[k] }
	if _, e := loadConfig(get, false); e != nil {
		t.Fatal(e)
	}
	if _, e := loadConfig(get, true); e == nil {
		t.Fatal("missing token accepted")
	}
	path := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(path, []byte("123:TEST\n"), 0600); e != nil {
		t.Fatal(e)
	}
	values["TELEGRAM_BOT_TOKEN_FILE"] = path
	c, e := loadConfig(get, true)
	if e != nil || c.Token != "123:TEST" {
		t.Fatal(e)
	}
	values["TELEGRAM_BOT_TOKEN"] = "456:OTHER"
	if _, e = loadConfig(get, true); e == nil {
		t.Fatal("ambiguous secret sources accepted")
	}
}
