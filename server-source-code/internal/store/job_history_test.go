package store

import (
	"context"
	"testing"
)

// The checks run before any database access.
func TestRecordOutcomeOfRefusesBadOutcomes(t *testing.T) {
	s := NewJobHistoryStore(nil)
	ok := CommandOutcome{JobID: "j1", ApiID: "api-a", QueueName: "agent-commands", JobNames: []string{"reboot"}, Status: JobCompleted}

	bad := ok
	bad.Status = JobActive // an outcome never reopens a record
	if _, err := s.RecordOutcomeOf(context.Background(), bad); err == nil {
		t.Error("accepted status active")
	}
	bad = ok
	bad.Status = "cancelled"
	if _, err := s.RecordOutcomeOf(context.Background(), bad); err == nil {
		t.Error("accepted an unknown status")
	}
	bad = ok
	bad.ApiID = ""
	if _, err := s.RecordOutcomeOf(context.Background(), bad); err == nil {
		t.Error("accepted an outcome without the agent")
	}
	bad = ok
	bad.JobID = ""
	if _, err := s.RecordOutcomeOf(context.Background(), bad); err == nil {
		t.Error("accepted an outcome without the job")
	}
	bad = ok
	bad.JobNames = nil
	if _, err := s.RecordOutcomeOf(context.Background(), bad); err == nil {
		t.Error("accepted an outcome that may change any job")
	}
}
