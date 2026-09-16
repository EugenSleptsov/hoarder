package item

import "errors"

const (
	Pause       Kind = "pause"
	Resume      Kind = "resume"
	Archive     Kind = "archive"
	Restore     Kind = "restore"
	Reconfigure Kind = "reconfigure"
)

// applyControl does not touch stock anchors, rate, samples or observation dates.
// Its timestamp only prevents an older command from crossing a later control.
func (s State) applyControl(e Event) (State, error) {
	if e.Quantity != (Interval{}) || e.HistoryComplete || !e.From.IsZero() {
		return s, errors.New("control is not physical stock evidence")
	}
	n := s
	switch e.Kind {
	case Pause:
		if s.Paused || s.Archived {
			return s, errors.New("item is not active")
		}
		n.Paused = true
	case Resume:
		if !s.Paused || s.Archived {
			return s, errors.New("only a paused non-archived item can resume")
		}
		n.Paused = false
	case Archive:
		if s.Archived {
			return s, errors.New("item already archived")
		}
		n.Archived, n.Paused = true, true
	case Restore:
		if !s.Archived {
			return s, errors.New("item is not archived")
		}
		// Restoration never silently resumes notifications.
		n.Archived, n.Paused = false, true
	case Reconfigure:
		if s.Archived || e.Configuration == nil || e.Configuration.ID != s.Config.ID {
			return s, errors.New("invalid configuration change")
		}
		if err := e.Configuration.Validate(); err != nil {
			return s, err
		}
		n.Config = *e.Configuration
	default:
		return s, errors.New("unsupported control")
	}
	at := e.At
	n.ControlAt = &at
	n.LastNote = "explicit_" + string(e.Kind) + "_without_stock_observation"
	return n, nil
}
