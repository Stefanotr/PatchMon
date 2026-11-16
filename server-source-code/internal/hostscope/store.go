package hostscope

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/jackc/pgx/v5"
)

// RoleScope is a role limited to some host groups.
type RoleScope struct {
	Role         string    `json:"role"`
	HostGroupIDs []string  `json:"host_group_ids"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// statusTTL is how long a database's Check result is reused.
const statusTTL = 30 * time.Second

type cachedStatus struct {
	status Status
	at     time.Time
}

// Store reads and writes the role scopes (table role_host_scopes, created by
// internal/forkschema). Plain SQL: the table belongs to this fork.
type Store struct {
	db database.DBProvider

	mu       sync.Mutex
	statuses map[*database.DB]cachedStatus
}

// NewStore returns a Store on db.
func NewStore(db database.DBProvider) *Store {
	return &Store{db: db, statuses: map[*database.DB]cachedStatus{}}
}

// ForRole returns the host groups role is limited to, and whether it is
// limited at all. A limited role with no group left sees no host.
func (s *Store) ForRole(ctx context.Context, role string) ([]string, bool, error) {
	var groups []string
	err := s.db.DB(ctx).RawQueryRow(ctx,
		`SELECT host_group_ids FROM role_host_scopes WHERE role = $1`, role).Scan(&groups)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if groups == nil {
		groups = []string{}
	}
	return groups, true, nil
}

// List returns every limited role, by name.
func (s *Store) List(ctx context.Context) ([]RoleScope, error) {
	rows, err := s.db.DB(ctx).Raw(ctx,
		`SELECT role, host_group_ids, updated_at FROM role_host_scopes ORDER BY role`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RoleScope{}
	for rows.Next() {
		var rs RoleScope
		if err := rows.Scan(&rs.Role, &rs.HostGroupIDs, &rs.UpdatedAt); err != nil {
			return nil, err
		}
		if rs.HostGroupIDs == nil {
			rs.HostGroupIDs = []string{}
		}
		out = append(out, rs)
	}
	return out, rows.Err()
}

// Limit limits role to the host groups groupIDs, replacing any previous list.
func (s *Store) Limit(ctx context.Context, role string, groupIDs []string, by string) error {
	var updatedBy *string
	if by != "" {
		updatedBy = &by
	}
	_, err := s.db.DB(ctx).Exec(ctx, `INSERT INTO role_host_scopes (role, host_group_ids, updated_by, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (role) DO UPDATE SET host_group_ids = EXCLUDED.host_group_ids,
			updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at`,
		role, groupIDs, updatedBy, time.Now().UTC())
	return err
}

// Unlimit gives role every host again.
func (s *Store) Unlimit(ctx context.Context, role string) error {
	_, err := s.db.DB(ctx).Exec(ctx, `DELETE FROM role_host_scopes WHERE role = $1`, role)
	return err
}

// MissingGroups returns the ids among groupIDs that are not host groups.
func (s *Store) MissingGroups(ctx context.Context, groupIDs []string) ([]string, error) {
	rows, err := s.db.DB(ctx).Raw(ctx, `SELECT x.id FROM unnest($1::text[]) AS x(id)
		WHERE NOT EXISTS (SELECT 1 FROM host_groups g WHERE g.id = x.id) ORDER BY x.id`, groupIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Status reports whether limited users can be served on the request's
// database (see Check), reusing a recent answer.
func (s *Store) Status(ctx context.Context) (Status, error) {
	d := s.db.DB(ctx)
	s.mu.Lock()
	c, ok := s.statuses[d]
	s.mu.Unlock()
	if ok && time.Since(c.at) < statusTTL {
		return c.status, nil
	}
	st, err := Check(ctx, d.Pool())
	if err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	s.statuses[d] = cachedStatus{status: st, at: time.Now()}
	s.mu.Unlock()
	return st, nil
}

// HostVisible reports whether the request can see the host id: always for an
// unlimited request whose host exists, for a limited one only within its
// groups (row security does the filtering).
func (s *Store) HostVisible(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := s.db.DB(ctx).RawQueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hosts WHERE id = $1)`, id).Scan(&ok)
	return ok, err
}
