package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
	output, err := marshalOutput(j.Output)
	if err != nil {
		return err
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

// Statuses of a command recorded with RecordStarted. job_history already
// uses them for queued jobs, and the Agent Activity shows them.
const (
	// JobActive: sent, or about to be, and the answer is awaited.
	JobActive = "active"
	// JobWaiting: not answered in time; a late answer still records the
	// outcome, until the patch-run cleanup fails the record (10 minutes
	// after the command was sent).
	JobWaiting   = "waiting"
	JobCompleted = "completed"
	JobFailed    = "failed"
)

// StartedJob is a command that does not go through the queue (a host restart,
// say), recorded before it is sent so that the record exists whatever
// happens next.
type StartedJob struct {
	JobID     string
	QueueName string
	JobName   string
	HostID    string
	ApiID     string
	// Output is stored as the job's JSON output (what was asked, by whom).
	Output map[string]any
}

// RecordStarted inserts the row of a command about to be sent (JobActive).
func (s *JobHistoryStore) RecordStarted(ctx context.Context, j StartedJob) error {
	output, err := marshalOutput(j.Output)
	if err != nil {
		return err
	}
	hostID, apiID := j.HostID, j.ApiID
	return s.db.DB(ctx).Queries.InsertJobHistoryStarted(ctx, db.InsertJobHistoryStartedParams{
		ID:        uuid.NewString(),
		JobID:     j.JobID,
		QueueName: j.QueueName,
		JobName:   j.JobName,
		HostID:    &hostID,
		ApiID:     &apiID,
		Output:    output,
	})
}

// CommandOutcome is what became of a command recorded with RecordStarted.
type CommandOutcome struct {
	JobID string
	// ApiID is the agent the command was sent to: the row of another host is
	// never changed.
	ApiID     string
	QueueName string
	// JobNames bounds the rows that may change to these jobs.
	JobNames []string
	// Status is JobCompleted, JobFailed or JobWaiting.
	Status string
	// Message is the failure, or why the outcome is still unknown.
	Message string
	// Output keys are merged into the recorded output.
	Output map[string]any
}

// RecordOutcomeOf records the outcome of a command on its row. The database
// enforces the transitions: JobActive to JobCompleted, JobFailed or
// JobWaiting, and JobWaiting to JobCompleted or JobFailed only. It reports
// whether a row changed: false when that agent has no such row in a state
// that allows the change.
func (s *JobHistoryStore) RecordOutcomeOf(ctx context.Context, o CommandOutcome) (bool, error) {
	switch o.Status {
	case JobCompleted, JobFailed, JobWaiting:
	default:
		return false, fmt.Errorf("invalid command outcome status %q", o.Status)
	}
	if o.JobID == "" || o.ApiID == "" || o.QueueName == "" || len(o.JobNames) == 0 {
		return false, errors.New("a command outcome needs the job, agent, queue and job names")
	}
	output, err := marshalOutput(o.Output)
	if err != nil {
		return false, err
	}
	var message *string
	if o.Message != "" {
		message = &o.Message
	}
	n, err := s.db.DB(ctx).Queries.UpdateAgentCommandOutcome(ctx, db.UpdateAgentCommandOutcomeParams{
		Status:       o.Status,
		ErrorMessage: message,
		Output:       output,
		JobID:        o.JobID,
		ApiID:        o.ApiID,
		QueueName:    o.QueueName,
		JobNames:     o.JobNames,
	})
	return n > 0, err
}

func marshalOutput(out map[string]any) ([]byte, error) {
	if len(out) == 0 {
		return nil, nil
	}
	return json.Marshal(out)
}
