package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/bot"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
	"github.com/EugenSleptsov/hoarder/internal/telegram"
)

type config struct {
	Owner       int64
	Path, Token string
	Schedule    bot.Config
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
	c.Token = strings.TrimSpace(get("TELEGRAM_BOT_TOKEN"))
	file := get("TELEGRAM_BOT_TOKEN_FILE")
	if file != "" {
		if c.Token != "" {
			return c, errors.New("set only one token source")
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return c, errors.New("cannot read token file")
		}
		c.Token = strings.TrimSpace(string(b))
	}
	if c.Token == "" {
		return c, errors.New("TELEGRAM_BOT_TOKEN or TELEGRAM_BOT_TOKEN_FILE required")
	}
	return c, nil
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
