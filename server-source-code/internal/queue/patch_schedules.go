package queue

import (
	"context"
	"errors"
	"log/slog"
	"time"

	hostctx "github.com/PatchMon/PatchMon/server-source-code/internal/context"
	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/patchschedule"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// Patch schedules (internal/patchschedule): a tick every minute fires the
// schedules that are due.
const (
	TypePatchScheduleTick = "patch-schedule-tick"
	QueuePatchSchedules   = "patch-schedules"

	// patchScheduleUniqueWindow keeps ticks from piling up behind a slow one.
	patchScheduleUniqueWindow = 2 * time.Minute
	patchScheduleRetention    = time.Hour
)

// RunPatchJobID is the job ID a patch run is recorded with, the asynq task ID
// of its run_patch task.
func RunPatchJobID(patchRunID string) string {
	return "patch-run-" + patchRunID
}

// ScheduledDryRunStarter records the dry runs of patch schedules and queues
// them to their hosts, the way POST /patching/trigger does a dry run.
// tenantHost is the context host of the database the runs go to ("" for the
// default one).
func ScheduledDryRunStarter(runs *store.PatchRunsStore, qc *asynq.Client, tenantHost string, log *slog.Logger) patchschedule.Starter {
	return func(ctx context.Context, t patchschedule.Target, packages []string, snapshot []byte, triggeredBy *string) error {
		if qc == nil {
			return errors.New("patch queue is not available")
		}
		runID := uuid.New().String()
		if _, err := runs.CreateRun(ctx, runID, t.HostID, RunPatchJobID(runID), "patch_package", nil, packages,
			triggeredBy, true, nil, nil, nil, snapshot, nil); err != nil {
			return err
		}
		task, err := NewRunPatchTask(RunPatchPayload{
			HostID:       t.HostID,
			Host:         tenantHost,
			ApiID:        t.ApiID,
			PatchRunID:   runID,
			PatchType:    "patch_package",
			PackageNames: packages,
			DryRun:       true,
		})
		if err == nil {
			_, err = qc.Enqueue(task)
		}
		if err != nil {
			// Leave no run waiting for a task that does not exist.
			if _, cerr := runs.Cancel(context.WithoutCancel(ctx), runID, "Failed to queue patch run"); cerr != nil && log != nil {
				log.Error("patch schedule: cancel unqueued run", "patch_run_id", runID, "error", cerr)
			}
			return err
		}
		return nil
	}
}

// PatchScheduleTickHandler fires the patch schedules that are due, in every
// database.
type PatchScheduleTickHandler struct {
	defaultDB *database.DB
	poolCache *hostctx.PoolCache
	qc        *asynq.Client
	log       *slog.Logger
}

// NewPatchScheduleTickHandler creates the handler.
func NewPatchScheduleTickHandler(defaultDB *database.DB, poolCache *hostctx.PoolCache, qc *asynq.Client, log *slog.Logger) *PatchScheduleTickHandler {
	return &PatchScheduleTickHandler{defaultDB: defaultDB, poolCache: poolCache, qc: qc, log: log}
}

// ProcessTask implements asynq.Handler.
func (h *PatchScheduleTickHandler) ProcessTask(ctx context.Context, _ *asynq.Task) error {
	now := time.Now()
	forEachDB(ctx, h.defaultDB, h.poolCache, func(ctx context.Context, d *database.DB, host string) {
		f := &patchschedule.Firer{
			Store: patchschedule.NewStore(d),
			Start: ScheduledDryRunStarter(store.NewPatchRunsStore(d), h.qc, host, h.log),
			Log:   h.log,
		}
		f.RunDue(ctx, patchschedule.OrgLocation(ctx, d), now)
	})
	return nil
}
