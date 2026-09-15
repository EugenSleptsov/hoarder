// hoarder-sim is an offline scenario, not a Telegram bot daemon.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/app"
	"github.com/EugenSleptsov/hoarder/internal/forecast"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/schedule"
)

type row struct {
	Phase    string            `json:"phase"`
	Forecast forecast.Decision `json:"forecast"`
	Question app.QuestionPlan  `json:"question"`
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(w io.Writer) error {
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	daily, err := schedule.New("Europe/Berlin", 21, 0)
	if err != nil {
		return err
	}
	paste, err := item.New(item.Config{ID: "paste", Name: "Toothpaste", WithReserve: true, ReserveUnits: 1, LeadDays: 3, MaxCheckDays: 60},
		item.Interval{Low: 2, High: 2}, item.Interval{Low: 0.03, High: 0.04}, start)
	if err != nil {
		return err
	}
	soap, err := item.New(item.Config{ID: "soap", Name: "Soap", LeadDays: 2, MaxCheckDays: 30},
		item.Interval{Low: 1, High: 1}, item.Interval{Low: 0.03, High: 0.04}, start)
	if err != nil {
		return err
	}
	var rows []row
	report := func(phase string, at time.Time) error {
		for _, s := range []item.State{paste, soap} {
			d, err := (forecast.Baseline{}).Predict(s, at)
			if err != nil {
				return err
			}
			q, err := app.PlanItem(s, forecast.Baseline{}, daily, at)
			if err != nil {
				return err
			}
			rows = append(rows, row{phase, d, q})
		}
		return nil
	}
	if err = report("initial", start); err != nil {
		return err
	}
	for _, e := range []item.Event{
		{ID: "check-20", ItemID: "paste", Kind: item.Snapshot, At: start.Add(20 * 24 * time.Hour), Quantity: item.Interval{Low: 1.1, High: 1.3}, HistoryComplete: true},
		{ID: "purchase-26", ItemID: "paste", Kind: item.Addition, At: start.Add(26 * 24 * time.Hour), Quantity: item.Interval{Low: 1, High: 1}},
		{ID: "unknown-30", ItemID: "paste", Kind: item.Unknown, At: start.Add(30 * 24 * time.Hour)},
	} {
		paste, err = paste.Apply(e)
		if err != nil {
			return err
		}
		if err = report(e.ID, e.At); err != nil {
			return err
		}
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(rows)
}
