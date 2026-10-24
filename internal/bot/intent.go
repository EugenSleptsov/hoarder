package bot

import (
	"context"
	"errors"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/forecast"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

// recommends applies the shopping-list rule to one item at one moment.
func recommends(state item.State, at time.Time) (bool, forecast.Decision, error) {
	decision, err := (forecast.Baseline{}).Predict(state, at)
	if err != nil {
		return false, decision, err
	}
	return decision.Stock.Mid() <= state.Config.ReserveUnits || decision.PossibleShortageBeforePurchase, decision, nil
}

// updateIntent reconciles a recommendation only. It never records a purchase,
// creates a physical observation or touches another item's plan.
func (s *Service) updateIntent(ctx context.Context, tx *sqlstore.Tx, state item.State, now time.Time) (bool, error) {
	needed, _, err := recommends(state, now)
	if err != nil {
		return false, err
	}
	if !needed {
		return false, tx.Delete(ctx, "intent", state.Config.ID)
	}
	var previous intent
	err = tx.Get(ctx, "intent", state.Config.ID, &previous)
	if errors.Is(err, sqlstore.ErrNotFound) {
		err = tx.Insert(ctx, "intent", state.Config.ID, intent{state.Config.ID, now})
	}
	return true, err
}
