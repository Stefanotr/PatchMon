package patchschedule

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned for a schedule that does not exist.
var ErrNotFound = errors.New("patch schedule not found")

// Store reads and writes patch schedules (table patch_schedules, created by
// internal/forkschema). Plain SQL: the table belongs to this fork, so its
// queries stay out of the sqlc files shared with upstream.
type Store struct {
	db database.DBProvider
}

// NewStore returns a Store on db.
func NewStore(db database.DBProvider) *Store {
	return &Store{db: db}
}

const scheduleColumns = `s.id, s.name, s.host_group_id, g.name, s.scope, s.frequency, s.weekdays,
	s.month_week, s.month_weekday, s.time_of_day, s.enabled, s.next_run_at, s.last_run_at,
	s.last_result, s.created_by, s.created_at, s.updated_at
	FROM patch_schedules s LEFT JOIN host_groups g ON g.id = s.host_group_id`

type scanner interface {
	Scan(dest ...any) error
}

func scanSchedule(row scanner) (Schedule, error) {
	var s Schedule
	var weekdays []int32
	var result []byte
	if err := row.Scan(&s.ID, &s.Name, &s.HostGroupID, &s.HostGroupName, &s.Scope, &s.Frequency, &weekdays,
		&s.MonthWeek, &s.MonthWeekday, &s.TimeOfDay, &s.Enabled, &s.NextRunAt, &s.LastRunAt,
		&result, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return Schedule{}, err
	}
	s.Weekdays = make([]int, len(weekdays))
	for i, d := range weekdays {
		s.Weekdays[i] = int(d)
	}
	if len(result) > 0 {
		var r Result
		if json.Unmarshal(result, &r) == nil {
			s.LastResult = &r
		}
	}
	return s, nil
}

func (st *Store) list(ctx context.Context, where string, args ...any) ([]Schedule, error) {
	rows, err := st.db.DB(ctx).Raw(ctx, "SELECT "+scheduleColumns+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// List returns every schedule, by name.
func (st *Store) List(ctx context.Context) ([]Schedule, error) {
	return st.list(ctx, "ORDER BY s.name, s.id")
}

// Get returns the schedule id, or ErrNotFound.
func (st *Store) Get(ctx context.Context, id string) (*Schedule, error) {
	s, err := scanSchedule(st.db.DB(ctx).RawQueryRow(ctx, "SELECT "+scheduleColumns+" WHERE s.id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Due returns the enabled schedules whose next slot is at or before now.
func (st *Store) Due(ctx context.Context, now time.Time) ([]Schedule, error) {
	return st.list(ctx, `WHERE s.enabled AND s.next_run_at IS NOT NULL AND s.next_run_at <= $1
		ORDER BY s.next_run_at LIMIT 100`, now.UTC())
}

func weekdays32(days []int) []int32 {
	out := make([]int32, len(days))
	for i, d := range days {
		out[i] = int32(d)
	}
	return out
}

// Create stores s, whose ID, NextRunAt and CreatedBy the caller has set.
func (st *Store) Create(ctx context.Context, s *Schedule, now time.Time) error {
	_, err := st.db.DB(ctx).Exec(ctx, `INSERT INTO patch_schedules
		(id, name, host_group_id, scope, frequency, weekdays, month_week, month_weekday,
		 time_of_day, enabled, next_run_at, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13)`,
		s.ID, s.Name, s.HostGroupID, s.Scope, s.Frequency, weekdays32(s.Weekdays), s.MonthWeek, s.MonthWeekday,
		s.TimeOfDay, s.Enabled, utcPtr(s.NextRunAt), s.CreatedBy, now.UTC())
	return err
}

// Update replaces the settings of s and its next slot, or returns ErrNotFound.
func (st *Store) Update(ctx context.Context, s *Schedule, now time.Time) error {
	n, err := st.db.DB(ctx).Exec(ctx, `UPDATE patch_schedules SET
		name = $2, host_group_id = $3, scope = $4, frequency = $5, weekdays = $6, month_week = $7,
		month_weekday = $8, time_of_day = $9, enabled = $10, next_run_at = $11, updated_at = $12
		WHERE id = $1`,
		s.ID, s.Name, s.HostGroupID, s.Scope, s.Frequency, weekdays32(s.Weekdays), s.MonthWeek,
		s.MonthWeekday, s.TimeOfDay, s.Enabled, utcPtr(s.NextRunAt), now.UTC())
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the schedule id, or returns ErrNotFound. Runs it started
// stay in the run history.
func (st *Store) Delete(ctx context.Context, id string) error {
	n, err := st.db.DB(ctx).Exec(ctx, `DELETE FROM patch_schedules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Claim moves the schedule id from its slot at to next, and reports whether
// this call did: of several server instances ticking together, one fires.
func (st *Store) Claim(ctx context.Context, id string, at, next, now time.Time) (bool, error) {
	n, err := st.db.DB(ctx).Exec(ctx, `UPDATE patch_schedules SET next_run_at = $3, last_run_at = $4
		WHERE id = $1 AND enabled AND next_run_at = $2`, id, at.UTC(), next.UTC(), now.UTC())
	return n == 1, err
}

// SetResult records what the last firing of the schedule id did.
func (st *Store) SetResult(ctx context.Context, id string, r Result) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = st.db.DB(ctx).Exec(ctx, `UPDATE patch_schedules SET last_result = $2::jsonb, last_run_at = $3
		WHERE id = $1`, id, string(b), r.FiredAt.UTC())
	return err
}

// Target is a host a schedule dry-runs.
type Target struct {
	HostID         string
	ApiID          string
	Name           string
	OSType         string
	PackageManager *string
}

// Targets returns the active hosts of the group, or every active host when
// groupID is nil.
func (st *Store) Targets(ctx context.Context, groupID *string) ([]Target, error) {
	rows, err := st.db.DB(ctx).Raw(ctx, `SELECT h.id, h.api_id, h.friendly_name, h.os_type, h.package_manager
		FROM hosts h
		WHERE h.status = 'active'
		  AND ($1::text IS NULL OR EXISTS (
			SELECT 1 FROM host_group_memberships m WHERE m.host_id = h.id AND m.host_group_id = $1::text))
		ORDER BY h.friendly_name, h.id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Target
	for rows.Next() {
		var t Target
		if err := rows.Scan(&t.HostID, &t.ApiID, &t.Name, &t.OSType, &t.PackageManager); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PendingPackages returns up to limit packages with an update waiting on the
// host, security updates first; only those when securityOnly.
func (st *Store) PendingPackages(ctx context.Context, hostID string, securityOnly bool, limit int) ([]string, error) {
	rows, err := st.db.DB(ctx).Raw(ctx, `SELECT p.name
		FROM host_packages hp JOIN packages p ON p.id = hp.package_id
		WHERE hp.host_id = $1 AND hp.needs_update AND (NOT $2::boolean OR hp.is_security_update)
		ORDER BY hp.is_security_update DESC, p.name
		LIMIT $3`, hostID, securityOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// HostGroupExists reports whether the host group id exists.
func (st *Store) HostGroupExists(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := st.db.DB(ctx).RawQueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM host_groups WHERE id = $1)`, id).Scan(&ok)
	return ok, err
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
