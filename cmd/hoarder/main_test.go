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

func TestMCPConfigRequiresLoopbackAndStrongToken(t *testing.T) {
	values := map[string]string{"HOARDER_CHAT_ID": "123", "TELEGRAM_BOT_TOKEN": "123:TEST"}
	get := func(k string) string { return values[k] }
	if c, e := loadConfig(get, true); e != nil || c.MCPAddr != "" {
		t.Fatal("MCP must stay disabled by default", c.MCPAddr, e)
	}
	strong := "0123456789abcdef0123456789abcdef"
	values["HOARDER_MCP_ADDR"] = "127.0.0.1:8765"
	if _, e := loadConfig(get, true); e == nil {
		t.Fatal("MCP without token accepted")
	}
	values["HOARDER_MCP_TOKEN"] = "short"
	if _, e := loadConfig(get, true); e == nil {
		t.Fatal("weak MCP token accepted")
	}
	values["HOARDER_MCP_TOKEN"] = strong
	for _, addr := range []string{"127.0.0.1:8765", "[::1]:8765", "localhost:8765"} {
		values["HOARDER_MCP_ADDR"] = addr
		if c, e := loadConfig(get, true); e != nil || c.MCPToken != strong {
			t.Fatal(addr, e)
		}
	}
	for _, addr := range []string{"0.0.0.0:8765", ":8765", "203.0.113.10:8765", "example.com:8765", "127.0.0.1"} {
		values["HOARDER_MCP_ADDR"] = addr
		if _, e := loadConfig(get, true); e == nil {
			t.Fatal("non-loopback MCP address accepted", addr)
		}
	}
	path := filepath.Join(t.TempDir(), "mcp-token")
	if e := os.WriteFile(path, []byte(strong+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	values["HOARDER_MCP_ADDR"] = "127.0.0.1:8765"
	values["HOARDER_MCP_TOKEN_FILE"] = path
	if _, e := loadConfig(get, true); e == nil {
		t.Fatal("ambiguous MCP token sources accepted")
	}
	delete(values, "HOARDER_MCP_TOKEN")
	if c, e := loadConfig(get, true); e != nil || c.MCPToken != strong {
		t.Fatal(e)
	}
}
