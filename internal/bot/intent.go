package bot

import (
	"context"
	"errors"
	"time"

	"github.com/EugenSleptsov/hoarder/internal/forecast"
	"github.com/EugenSleptsov/hoarder/internal/item"
	"github.com/EugenSleptsov/hoarder/internal/sqlstore"
)

// updateIntent reconciles a recommendation only. It never records a purchase,
// creates a physical observation or touches another item's plan.
func (s *Service) updateIntent(ctx context.Context, tx *sqlstore.Tx, state item.State, now time.Time) (bool, error) {
	decision, err := (forecast.Baseline{}).Predict(state, now)
	if err != nil {
		return false, err
	}
	needed := decision.Stock.Mid() <= state.Config.ReserveUnits || decision.PossibleShortageBeforePurchase
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
