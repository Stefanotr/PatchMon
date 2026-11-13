package patchschedule

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/config"
	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

// MissedAfter: a slot found more than this late (the server was down) is
// skipped and recorded as missed, rather than dry-running hours later.
const MissedAfter = time.Hour

// Starter records a dry run of packages on t and queues it to the host. It
// returns store.ErrHostHasActivePatchRun when the host already has a run
// queued, validating or running.
type Starter func(ctx context.Context, t Target, packages []string, snapshot []byte, triggeredBy *string) error

// Storage is what firing needs from the schedule store (*Store).
type Storage interface {
	Due(ctx context.Context, now time.Time) ([]Schedule, error)
	Claim(ctx context.Context, id string, at, next, now time.Time) (bool, error)
	SetResult(ctx context.Context, id string, r Result) error
	Targets(ctx context.Context, groupID *string) ([]Target, error)
	PendingPackages(ctx context.Context, hostID string, securityOnly bool, limit int) ([]string, error)
}

// Firer fires schedules.
type Firer struct {
	Store Storage
	Start Starter
	Log   *slog.Logger
}

func (f *Firer) log() *slog.Logger {
	if f.Log == nil {
		return slog.Default()
	}
	return f.Log
}

// snapshot is what a scheduled run records in its policy snapshot, for the
// run details and for whoever approves it.
func snapshot(s *Schedule, manual bool) []byte {
	b, _ := json.Marshal(map[string]interface{}{
		"patch_delay_type": "immediate",
		"schedule": map[string]interface{}{
			"id":     s.ID,
			"name":   s.Name,
			"scope":  s.Scope,
			"manual": manual,
		},
	})
	return b
}

// Fire queues a dry run of the pending updates in scope on every active host
// of s. triggeredBy is the user of a "Run now", nil for a slot.
func (f *Firer) Fire(ctx context.Context, s *Schedule, triggeredBy *string, now time.Time) (Result, error) {
	res := Result{FiredAt: now.UTC(), Manual: triggeredBy != nil}
	targets, err := f.Store.Targets(ctx, s.HostGroupID)
	if err != nil {
		return res, err
	}
	snap := snapshot(s, triggeredBy != nil)
	for _, t := range targets {
		// A Windows run or a pacman host cannot dry-run a list of packages.
		if store.IsWindowsOSType(t.OSType) || store.RefusesSelectedPatch(t.PackageManager, "patch_package") {
			res.Unsupported++
			continue
		}
		pkgs, err := f.Store.PendingPackages(ctx, t.HostID, s.Scope == ScopeSecurity, MaxPackages)
		if err != nil {
			res.Failed++
			f.log().Warn("patch schedule: pending packages", "schedule_id", s.ID, "host_id", t.HostID, "error", err)
			continue
		}
		if len(pkgs) == 0 {
			res.UpToDate++
			continue
		}
		switch err := f.Start(ctx, t, pkgs, snap, triggeredBy); {
		case err == nil:
			res.Queued++
		case errors.Is(err, store.ErrHostHasActivePatchRun):
			res.Busy++
		default:
			res.Failed++
			f.log().Warn("patch schedule: dry run not queued", "schedule_id", s.ID, "host_id", t.HostID, "error", err)
		}
	}
	return res, nil
}

// RunDue fires the schedules due at now, each once whatever the number of
// server instances, and moves each to its next slot in loc. A slot more than
// MissedAfter late is recorded as missed instead.
func (f *Firer) RunDue(ctx context.Context, loc *time.Location, now time.Time) {
	due, err := f.Store.Due(ctx, now)
	if err != nil {
		f.log().Warn("patch schedule: list due schedules", "error", err)
		return
	}
	for i := range due {
		s := &due[i]
		at := *s.NextRunAt
		next, err := s.Next(now, loc)
		if err != nil {
			f.log().Warn("patch schedule: next slot", "schedule_id", s.ID, "error", err)
			continue
		}
		claimed, err := f.Store.Claim(ctx, s.ID, at, next, now)
		if err != nil {
			f.log().Warn("patch schedule: claim", "schedule_id", s.ID, "error", err)
			continue
		}
		if !claimed {
			continue
		}
		res := Result{FiredAt: now.UTC(), Missed: true}
		if now.Sub(at) <= MissedAfter {
			if res, err = f.Fire(ctx, s, nil, now); err != nil {
				res.Failed++
				f.log().Warn("patch schedule: fire", "schedule_id", s.ID, "error", err)
			}
		}
		if err := f.Store.SetResult(ctx, s.ID, res); err != nil {
			f.log().Warn("patch schedule: record result", "schedule_id", s.ID, "error", err)
		}
		f.log().Info("patch schedule fired", "schedule_id", s.ID, "missed", res.Missed,
			"queued", res.Queued, "up_to_date", res.UpToDate, "busy", res.Busy,
			"unsupported", res.Unsupported, "failed", res.Failed, "next_run_at", next)
	}
}

// OrgLocation is the organisation's timezone (TZ or TIMEZONE in the
// environment, else Settings), the one patch policies use too; UTC when it
// cannot be loaded.
func OrgLocation(ctx context.Context, d *database.DB) *time.Location {
	var tz *string
	if s, err := d.Queries.GetFirstSettings(ctx); err == nil {
		tz = s.Timezone
	}
	loc, err := time.LoadLocation(config.ResolveTimezone(tz, nil))
	if err != nil {
		return time.UTC
	}
	return loc
}
