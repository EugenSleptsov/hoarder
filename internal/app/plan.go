// Package app connects the pure model to scheduling and defines persistence ports.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/forecast"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/schedule"
)

type QuestionPlan struct {
	ItemID            string    `json:"item_id"`
	ExpectedRevision  uint64    `json:"expected_revision"`
	ModelVersion      string    `json:"model_version"`
	RequestedDeadline time.Time `json:"requested_deadline"`
	SendAt            time.Time `json:"send_at"`
	LocalDate         string    `json:"local_date"`
	DeadlineUnmet     bool      `json:"deadline_unmet"`
}

// PlanItem cannot inspect other items; batching belongs exclusively to delivery.
func PlanItem(s item.State, p forecast.Predictor, daily schedule.Daily, now time.Time) (QuestionPlan, error) {
	if p == nil {
		return QuestionPlan{}, errors.New("predictor required")
	}
	decision, err := p.Predict(s, now)
	if err != nil {
		return QuestionPlan{}, err
	}
	if decision.ItemID != s.Config.ID {
		return QuestionPlan{}, errors.New("predictor returned a different item")
	}
	plan, err := daily.BeforeDeadline(now, decision.NextCheckAt)
	if err != nil {
		return QuestionPlan{}, err
	}
	date, err := daily.DateKey(plan.At)
	if err != nil {
		return QuestionPlan{}, err
	}
	return QuestionPlan{ItemID: s.Config.ID, ExpectedRevision: s.Revision, ModelVersion: decision.ModelVersion,
		RequestedDeadline: decision.NextCheckAt, SendAt: plan.At, LocalDate: date, DeadlineUnmet: plan.DeadlineUnmet}, nil
}

// The ports below specify the first durable vertical slice; no SQL implementation
// or background worker is included yet. A transaction must roll back all writes
// on error. Implementations must enforce ownership and uniqueness in the schema.
type Store interface {
	WithinTransaction(context.Context, func(Tx) error) error
}

type Tx interface {
	LoadItem(context.Context, string) (item.State, error)
	CommandReceipt(context.Context, string, string) (*Receipt, error)
	AppendEvent(context.Context, item.Event) error
	SaveProjection(context.Context, item.State, uint64) error
	SaveQuestion(context.Context, QuestionPlan) error
	Enqueue(context.Context, OutboxMessage) error
	SaveReceipt(context.Context, Receipt) error
}

type Receipt struct {
	ItemID         string
	CommandID      string
	PayloadHash    string
	ResultRevision uint64
}

type OutboxMessage struct {
	ID            string
	HouseholdID   string
	Kind          string
	Payload       json.RawMessage
	CreatedAt     time.Time
	NextAttemptAt time.Time
}
