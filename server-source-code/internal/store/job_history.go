package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
)

// JobHistoryStore updates job_history rows outside the queue workers.
type JobHistoryStore struct {
	db database.DBProvider
}

// NewJobHistoryStore creates a new job history store.
func NewJobHistoryStore(db database.DBProvider) *JobHistoryStore {
	return &JobHistoryStore{db: db}
}

// RecordOutcome marks a job completed, or failed with failure as the reason.
func (s *JobHistoryStore) RecordOutcome(ctx context.Context, jobID, failure string) error {
	d := s.db.DB(ctx)
	if failure == "" {
		return d.Queries.UpdateJobHistoryCompleted(ctx, jobID)
	}
	return d.Queries.UpdateJobHistoryFailed(ctx, db.UpdateJobHistoryFailedParams{JobID: jobID, ErrorMessage: &failure})
}

// FinishedJob is a command that did not go through the queue (a host restart,
// say), recorded once its outcome is known so it shows in the host's Agent
// Activity.
type FinishedJob struct {
	JobID     string
	QueueName string
	JobName   string
	HostID    string
	ApiID     string
	// Failure is empty when the job succeeded.
	Failure string
	// Output is stored as the job's JSON output.
	Output map[string]any
}

// RecordFinished inserts a completed or failed job_history row.
func (s *JobHistoryStore) RecordFinished(ctx context.Context, j FinishedJob) error {
	var output []byte
	if len(j.Output) > 0 {
		b, err := json.Marshal(j.Output)
		if err != nil {
			return err
		}
		output = b
	}
	status := "completed"
	var failure *string
	if j.Failure != "" {
		status, failure = "failed", &j.Failure
	}
	hostID, apiID := j.HostID, j.ApiID
	return s.db.DB(ctx).Queries.InsertJobHistoryOutcome(ctx, db.InsertJobHistoryOutcomeParams{
		ID:           uuid.NewString(),
		JobID:        j.JobID,
		QueueName:    j.QueueName,
		JobName:      j.JobName,
		HostID:       &hostID,
		ApiID:        &apiID,
		Status:       status,
		ErrorMessage: failure,
		Output:       output,
	})
}
