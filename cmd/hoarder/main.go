package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/bot"
	"github.com/EugenSleptsov/hoarder/internal/mcp"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

const mcpVersion = "0.1.0"

type config struct {
	Owner             int64
	Path, Token       string
	Schedule          bot.Config
	MCPAddr, MCPToken string
}

// readSecret accepts NAME or NAME_FILE, never both. Values are never logged.
func readSecret(get func(string) string, name string) (string, error) {
	value := strings.TrimSpace(get(name))
	if file := get(name + "_FILE"); file != "" {
		if value != "" {
			return "", fmt.Errorf("set only one of %s and %s_FILE", name, name)
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("cannot read %s_FILE", name)
		}
		value = strings.TrimSpace(string(b))
	}
	if value == "" {
		return "", fmt.Errorf("%s or %s_FILE required", name, name)
	}
	return value, nil
}

// loadMCP keeps the agent endpoint on loopback. Remote agents reach it only
// through an authenticated tunnel or TLS proxy, never a public plain port.
func loadMCP(get func(string) string, c *config) error {
	c.MCPAddr = get("HOARDER_MCP_ADDR")
	if c.MCPAddr == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(c.MCPAddr)
	if err != nil || port == "" {
		return errors.New("HOARDER_MCP_ADDR must be host:port")
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("HOARDER_MCP_ADDR must be a loopback address")
	}
	if c.MCPToken, err = readSecret(get, "HOARDER_MCP_TOKEN"); err != nil {
		return err
	}
	if len(c.MCPToken) < 32 {
		return errors.New("HOARDER_MCP_TOKEN must have at least 32 characters")
	}
	return nil
}

func loadConfig(get func(string) string, readToken bool) (config, error) {
	var c config
	var e error
	c.Owner, e = strconv.ParseInt(get("HOARDER_CHAT_ID"), 10, 64)
	if e != nil || c.Owner <= 0 {
		return c, errors.New("HOARDER_CHAT_ID must be the positive ID of the authorised private chat")
	}
	c.Path = get("HOARDER_DB")
	if c.Path == "" {
		c.Path = "data/hoarder.db"
	}
	c.Schedule.Zone = get("HOARDER_TIMEZONE")
	if c.Schedule.Zone == "" {
		c.Schedule.Zone = "Europe/Berlin"
	}
	clock := get("HOARDER_POLL_TIME")
	if clock == "" {
		clock = "21:00"
	}
	t, e := time.Parse("15:04", clock)
	if e != nil || t.Format("15:04") != clock {
		return c, errors.New("HOARDER_POLL_TIME must be HH:MM")
	}
	c.Schedule.Hour, c.Schedule.Minute = t.Hour(), t.Minute()
	if _, e = time.LoadLocation(c.Schedule.Zone); e != nil {
		return c, errors.New("invalid HOARDER_TIMEZONE")
	}
	grace := get("HOARDER_CATCH_UP")
	if grace == "" {
		grace = "30m"
	}
	c.Schedule.CatchUp, e = time.ParseDuration(grace)
	if e != nil || c.Schedule.CatchUp < time.Minute || c.Schedule.CatchUp > 4*time.Hour {
		return c, errors.New("HOARDER_CATCH_UP must be between 1m and 4h")
	}
	if !readToken {
		return c, nil
	}
	if c.Token, e = readSecret(get, "TELEGRAM_BOT_TOKEN"); e != nil {
		return c, e
	}
	return c, loadMCP(get, &c)
}

func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func retryDelay(err error) time.Duration {
	var api *telegram.APIError
	if errors.As(err, &api) && api.RetryAfterSeconds > 0 {
		return time.Duration(api.RetryAfterSeconds) * time.Second
	}
	return 2 * time.Second
}
func flush(ctx context.Context, s *bot.Service, api *telegram.Client) error {
	err := s.Flush(ctx, api, time.Now().UTC())
	var apiErr *telegram.APIError
	if errors.As(err, &apiErr) && apiErr.Code == 429 {
		return wait(ctx, retryDelay(err))
	}
	return err
}

func run(ctx context.Context, c config, backup string) error {
	db, e := sqlstore.Open(ctx, c.Path, c.Owner)
	if e != nil {
		return e
	}
	defer db.Close()
	if backup != "" {
		return db.Backup(ctx, backup)
	}
	s, e := bot.New(ctx, db, c.Schedule)
	if e != nil {
		return e
	}
	api, e := telegram.New(c.Token)
	if e != nil {
		return e
	}
	// Invitation links need the bot's @username. Without it the bot still
	// runs, and invitations show a /start code to send instead.
	meCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	name, e := api.Username(meCtx)
	cancel()
	if e != nil {
		slog.Warn("bot username unavailable; invitations show a code instead of a link", "error", e)
	}
	s.SetUsername(name)
	if c.MCPAddr != "" {
		stop, err := serveMCP(ctx, c.MCPAddr, c.MCPToken, s)
		if err != nil {
			return err
		}
		defer stop()
	}
	if e = s.BeginPolling(ctx); e != nil {
		return e
	}
	slog.Info("bot started", "timezone", c.Schedule.Zone, "hour", c.Schedule.Hour, "minute", c.Schedule.Minute)
	for ctx.Err() == nil {
		if e = s.Tick(ctx, time.Now().UTC()); e != nil {
			return e
		}
		if e = flush(ctx, s, api); e != nil {
			return e
		}
		offset, err := s.Offset(ctx)
		if err != nil {
			return err
		}
		updates, err := api.Updates(ctx, offset)
		if err != nil {
			var apiErr *telegram.APIError
			if errors.As(err, &apiErr) && (apiErr.Code == 401 || apiErr.Code == 409) {
				return fmt.Errorf("polling cannot continue (check token, webhook and single-instance deployment): %w", err)
			}
			if e = wait(ctx, retryDelay(err)); e != nil {
				return e
			}
			continue
		}
		if len(updates) == 0 {
			if e = s.CompleteEmptyPoll(ctx, offset); e != nil {
				return e
			}
		}
		for _, u := range updates {
			notice, err := s.Handle(ctx, u, time.Now().UTC())
			if u.Callback != nil && u.Callback.ID != "" {
				if err != nil {
					notice = "Ответ не сохранён. Повторите после восстановления бота."
				}
				ackCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				ackErr := api.AnswerNotice(ackCtx, u.Callback.ID, notice)
				cancel()
				if ackErr != nil {
					slog.Warn("callback acknowledgement failed", "update_id", u.ID, "error", ackErr)
				}
			}
			if err != nil {
				return fmt.Errorf("update processing failed: %w", err)
			}
			if e = flush(ctx, s, api); e != nil {
				return e
			}
		}
	}
	return ctx.Err()
}

// serveMCP binds before polling starts so a wrong address fails at startup.
// The endpoint shares this process and its single SQLite writer; agent writes
// are ordinary transactions, and queued messages go out with the next flush.
func serveMCP(ctx context.Context, addr, token string, s *bot.Service) (func(), error) {
	handler, err := mcp.New(mcp.Info{Name: "hoarder", Version: mcpVersion, Instructions: bot.AgentInstructions},
		token, s.AgentTools(func() time.Time { return time.Now().UTC() }))
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("MCP listener: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		// A failing agent endpoint must not stop household questions.
		if err := srv.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			slog.Error("MCP server stopped", "error", err)
		}
	}()
	slog.Info("MCP server started", "address", listener.Addr().String())
	return func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}, nil
}

func main() {
	backup := flag.String("backup", "", "write a consistent backup to a new file and exit")
	flag.Parse()
	c, e := loadConfig(os.Getenv, *backup == "")
	if e != nil {
		slog.Error("configuration failed", "error", e)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e = run(ctx, c, *backup); e != nil && !errors.Is(e, context.Canceled) {
		slog.Error("bot stopped", "error", e)
		os.Exit(1)
	}
}
