package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
)

// HostRemoteRestartStore keeps whether each host's agent allows remote
// restarts, as the agent announced it when it last connected. The agent
// alone decides (remote-restart-enabled in its config.yml): this only lets
// the UI grey the restart out where the host would refuse it.
type HostRemoteRestartStore struct {
	db database.DBProvider
}

// NewHostRemoteRestartStore creates the store.
func NewHostRemoteRestartStore(db database.DBProvider) *HostRemoteRestartStore {
	return &HostRemoteRestartStore{db: db}
}

// Record stores what the host's agent announced; nil means it said nothing
// (an agent that predates the announcement), which forgets any earlier
// answer.
func (s *HostRemoteRestartStore) Record(ctx context.Context, hostID string, enabled *bool) error {
	q := s.db.DB(ctx).Queries
	if enabled == nil {
		return q.DeleteHostRemoteRestart(ctx, hostID)
	}
	return q.UpsertHostRemoteRestart(ctx, db.UpsertHostRemoteRestartParams{HostID: hostID, Enabled: *enabled})
}

// Get returns what the host announced, nil when it has not said.
func (s *HostRemoteRestartStore) Get(ctx context.Context, hostID string) (*bool, error) {
	row, err := s.db.DB(ctx).Queries.GetHostRemoteRestart(ctx, hostID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row.Enabled, nil
}

// All returns the answer of every host that gave one, by host ID.
func (s *HostRemoteRestartStore) All(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.DB(ctx).Queries.ListHostRemoteRestart(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.HostID] = r.Enabled
	}
	return out, nil
}
