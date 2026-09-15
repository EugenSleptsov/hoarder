// Package schedule maps independent deadlines onto a shared daily wall-clock slot.
package schedule

import (
	"errors"
	"time"
	_ "time/tzdata"
)

type Daily struct {
	location *time.Location
	hour     int
	minute   int
}

func New(zone string, hour, minute int) (Daily, error) {
	if zone == "" || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return Daily{}, errors.New("IANA timezone and valid hour/minute are required")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return Daily{}, err
	}
	return Daily{loc, hour, minute}, nil
}

// Slot resolves a civil date in the configured zone. For a repeated wall minute
// use its first occurrence; for a missing minute use the first later minute.
// A skipped civil date is reported, not silently assigned to another date.
func (d Daily) Slot(year int, month time.Month, day int) (time.Time, error) {
	if d.location == nil || year < 1970 || year > 9998 {
		return time.Time{}, errors.New("invalid schedule or unsupported year")
	}
	date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if date.Year() != year || date.Month() != month || date.Day() != day {
		return time.Time{}, errors.New("invalid civil date")
	}
	// A small deterministic search avoids time.Date's unspecified choice when
	// a wall-clock time occurs twice. This reference version favours clarity.
	start := date.Add(-18 * time.Hour)
	for i := 0; i < 60*60; i++ {
		candidate := start.Add(time.Duration(i) * time.Minute)
		local := candidate.In(d.location)
		if local.Year() == year && local.Month() == month && local.Day() == day &&
			local.Hour()*60+local.Minute() >= d.hour*60+d.minute {
			return candidate, nil
		}
	}
	return time.Time{}, errors.New("no daily slot on this civil date")
}

// Next includes now if now is exactly the selected slot.
func (d Daily) Next(now time.Time) (time.Time, error) {
	if d.location == nil || now.IsZero() {
		return time.Time{}, errors.New("invalid schedule or time")
	}
	local := now.In(d.location)
	date := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		day := date.AddDate(0, 0, i)
		slot, err := d.Slot(day.Year(), day.Month(), day.Day())
		if err == nil && !slot.Before(now) {
			return slot, nil
		}
	}
	return time.Time{}, errors.New("no available daily slot")
}

type Plan struct {
	At            time.Time `json:"at"`
	DeadlineUnmet bool      `json:"deadline_unmet"`
}

// BeforeDeadline chooses the latest still-available slot not after the deadline.
// If none exists, it returns the next slot and explicitly reports the missed margin.
func (d Daily) BeforeDeadline(now, deadline time.Time) (Plan, error) {
	if d.location == nil || now.IsZero() || deadline.IsZero() {
		return Plan{}, errors.New("invalid schedule or time")
	}
	local := deadline.In(d.location)
	date := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		day := date.AddDate(0, 0, -i)
		slot, err := d.Slot(day.Year(), day.Month(), day.Day())
		if err == nil && !slot.After(deadline) && !slot.Before(now) {
			return Plan{At: slot}, nil
		}
	}
	next, err := d.Next(now)
	return Plan{At: next, DeadlineUnmet: true}, err
}

func (d Daily) DateKey(at time.Time) (string, error) {
	if d.location == nil || at.IsZero() {
		return "", errors.New("invalid schedule or time")
	}
	return at.In(d.location).Format("2006-01-02"), nil
}
