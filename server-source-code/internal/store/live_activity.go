package store

import (
	"context"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
	"github.com/PatchMon/PatchMon/server-source-code/internal/pgtime"
)

// LiveActivityStore reads what a host's agent is doing now, for the live
// part of its activity.
type LiveActivityStore struct {
	db database.DBProvider
}

// NewLiveActivityStore creates the store.
func NewLiveActivityStore(db database.DBProvider) *LiveActivityStore {
	return &LiveActivityStore{db: db}
}

// liveJobsMax bounds the job rows returned.
const liveJobsMax = 20

// Jobs returns the agent's jobs still open that started after openAfter,
// and those that ended after finishedAfter, newest first.
func (s *LiveActivityStore) Jobs(ctx context.Context, apiID string, openAfter, finishedAfter time.Time) ([]db.ListLiveJobHistoryRow, error) {
	return s.db.DB(ctx).Queries.ListLiveJobHistory(ctx, db.ListLiveJobHistoryParams{
		ApiID:         apiID,
		OpenAfter:     pgtime.From(openAfter),
		FinishedAfter: pgtime.From(finishedAfter),
		MaxRows:       liveJobsMax,
	})
}

// PatchRuns returns the host's patch runs queued or running, and those that
// ended after finishedAfter, newest first.
func (s *LiveActivityStore) PatchRuns(ctx context.Context, hostID string, finishedAfter time.Time) ([]db.ListLivePatchRunsRow, error) {
	return s.db.DB(ctx).Queries.ListLivePatchRuns(ctx, db.ListLivePatchRunsParams{
		HostID:        hostID,
		FinishedAfter: pgtime.From(finishedAfter),
	})
}
