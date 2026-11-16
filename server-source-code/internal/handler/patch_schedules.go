package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/middleware"
	"github.com/PatchMon/PatchMon/server-source-code/internal/patchschedule"
	"github.com/PatchMon/PatchMon/server-source-code/internal/queue"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// Audit events of patch schedules.
const (
	auditEventPatchScheduleCreate = "patch_schedule_create"
	auditEventPatchScheduleUpdate = "patch_schedule_update"
	auditEventPatchScheduleDelete = "patch_schedule_delete"
	auditEventPatchScheduleRun    = "patch_schedule_run"
)

// PatchSchedulesHandler serves /patching/schedules: recurring dry runs of a
// host group's pending updates, each approved by a person before anything is
// installed.
type PatchSchedulesHandler struct {
	db     database.DBProvider
	runs   *store.PatchRunsStore
	audits *store.AuditLogsStore
	qc     *asynq.Client
	log    *slog.Logger
	now    func() time.Time
}

// NewPatchSchedulesHandler creates the handler. audits may be nil.
func NewPatchSchedulesHandler(db database.DBProvider, runs *store.PatchRunsStore, audits *store.AuditLogsStore, qc *asynq.Client, log *slog.Logger) *PatchSchedulesHandler {
	if log == nil {
		log = slog.Default()
	}
	return &PatchSchedulesHandler{db: db, runs: runs, audits: audits, qc: qc, log: log, now: time.Now}
}

type patchScheduleBody struct {
	Name         string  `json:"name"`
	HostGroupID  *string `json:"host_group_id"`
	Scope        string  `json:"scope"`
	Frequency    string  `json:"frequency"`
	Weekdays     []int   `json:"weekdays"`
	MonthWeek    int     `json:"month_week"`
	MonthWeekday int     `json:"month_weekday"`
	TimeOfDay    string  `json:"time_of_day"`
	Enabled      *bool   `json:"enabled"`
}

func (h *PatchSchedulesHandler) store() *patchschedule.Store {
	return patchschedule.NewStore(h.db)
}

func userIDOf(r *http.Request) string {
	id, _ := r.Context().Value(middleware.UserIDKey).(string)
	return id
}

// audit records event on s in the security audit log.
func (h *PatchSchedulesHandler) audit(r *http.Request, event string, s *patchschedule.Schedule, extra map[string]any) {
	if h.audits == nil {
		return
	}
	meta := map[string]any{"schedule_id": s.ID, "name": s.Name}
	for k, v := range extra {
		meta[k] = v
	}
	if err := h.audits.Record(r.Context(), newAuditEntry(r, event, userIDOf(r), "", true, meta)); err != nil {
		h.log.Warn("patch schedules: audit record", "event", event, "schedule_id", s.ID, "error", err)
	}
}

// List handles GET /patching/schedules.
func (h *PatchSchedulesHandler) List(w http.ResponseWriter, r *http.Request) {
	schedules, err := h.store().List(r.Context())
	if err != nil {
		h.log.Error("patch schedules: list", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to load patch schedules"})
		return
	}
	loc := patchschedule.OrgLocation(r.Context(), h.db.DB(r.Context()))
	JSON(w, http.StatusOK, map[string]interface{}{"schedules": schedules, "timezone": loc.String()})
}

// apply copies body onto s, checks it, and sets the next slot: none while
// the schedule is disabled. It writes the error response and returns false
// on a mistake.
func (h *PatchSchedulesHandler) apply(w http.ResponseWriter, r *http.Request, s *patchschedule.Schedule, body patchScheduleBody) bool {
	s.Name = body.Name
	s.HostGroupID = nil
	if body.HostGroupID != nil && strings.TrimSpace(*body.HostGroupID) != "" {
		id := strings.TrimSpace(*body.HostGroupID)
		ok, err := h.store().HostGroupExists(r.Context(), id)
		if err != nil {
			h.log.Error("patch schedules: host group lookup", "error", err)
			JSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to check the host group"})
			return false
		}
		if !ok {
			JSON(w, http.StatusBadRequest, map[string]string{"error": "Host group not found"})
			return false
		}
		s.HostGroupID = &id
	}
	s.Scope = body.Scope
	s.Frequency = body.Frequency
	s.Weekdays = body.Weekdays
	s.MonthWeek = body.MonthWeek
	s.MonthWeekday = body.MonthWeekday
	s.TimeOfDay = strings.TrimSpace(body.TimeOfDay)
	if body.Enabled != nil {
		s.Enabled = *body.Enabled
	}
	s.Normalize()
	if err := s.Validate(); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return false
	}
	s.NextRunAt = nil
	if s.Enabled {
		loc := patchschedule.OrgLocation(r.Context(), h.db.DB(r.Context()))
		next, err := s.Next(h.now(), loc)
		if err != nil {
			JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return false
		}
		s.NextRunAt = &next
	}
	return true
}

// Create handles POST /patching/schedules.
func (h *PatchSchedulesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body patchScheduleBody
	if err := decodeJSON(r, &body); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	s := patchschedule.Schedule{ID: uuid.New().String(), Enabled: true}
	if !h.apply(w, r, &s, body) {
		return
	}
	if id := userIDOf(r); id != "" {
		s.CreatedBy = &id
	}
	if err := h.store().Create(r.Context(), &s, h.now()); err != nil {
		h.log.Error("patch schedules: create", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create the patch schedule"})
		return
	}
	h.audit(r, auditEventPatchScheduleCreate, &s, map[string]any{"enabled": s.Enabled})
	h.respond(w, r, http.StatusCreated, s.ID)
}

// Update handles PUT /patching/schedules/{id}. Saving a schedule moves it to
// its next slot from now.
func (h *PatchSchedulesHandler) Update(w http.ResponseWriter, r *http.Request) {
	s, ok := h.load(w, r)
	if !ok {
		return
	}
	var body patchScheduleBody
	if err := decodeJSON(r, &body); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}
	if !h.apply(w, r, s, body) {
		return
	}
	switch err := h.store().Update(r.Context(), s, h.now()); {
	case errors.Is(err, patchschedule.ErrNotFound):
		JSON(w, http.StatusNotFound, map[string]string{"error": "Patch schedule not found"})
		return
	case err != nil:
		h.log.Error("patch schedules: update", "schedule_id", s.ID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to update the patch schedule"})
		return
	}
	h.audit(r, auditEventPatchScheduleUpdate, s, map[string]any{"enabled": s.Enabled})
	h.respond(w, r, http.StatusOK, s.ID)
}

// Delete handles DELETE /patching/schedules/{id}. The runs the schedule
// started stay in the run history.
func (h *PatchSchedulesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	s, ok := h.load(w, r)
	if !ok {
		return
	}
	switch err := h.store().Delete(r.Context(), s.ID); {
	case errors.Is(err, patchschedule.ErrNotFound):
		JSON(w, http.StatusNotFound, map[string]string{"error": "Patch schedule not found"})
		return
	case err != nil:
		h.log.Error("patch schedules: delete", "schedule_id", s.ID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to delete the patch schedule"})
		return
	}
	h.audit(r, auditEventPatchScheduleDelete, s, nil)
	JSON(w, http.StatusOK, map[string]string{"message": "Patch schedule deleted"})
}

// RunNow handles POST /patching/schedules/{id}/run: the dry runs of the
// schedule, now, whether or not it is enabled. Its next slot is unchanged.
func (h *PatchSchedulesHandler) RunNow(w http.ResponseWriter, r *http.Request) {
	s, ok := h.load(w, r)
	if !ok {
		return
	}
	var by *string
	if id := userIDOf(r); id != "" {
		by = &id
	}
	st := h.store()
	f := &patchschedule.Firer{
		Store: st,
		Start: queue.ScheduledDryRunStarter(h.runs, h.qc, r.Header.Get("X-Forwarded-Host"), h.log),
		Log:   h.log,
	}
	res, err := f.Fire(r.Context(), s, by, h.now())
	if err != nil {
		h.log.Error("patch schedules: run now", "schedule_id", s.ID, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to run the patch schedule"})
		return
	}
	if err := st.SetResult(r.Context(), s.ID, res); err != nil {
		h.log.Warn("patch schedules: record result", "schedule_id", s.ID, "error", err)
	}
	h.audit(r, auditEventPatchScheduleRun, s, map[string]any{"queued": res.Queued})
	JSON(w, http.StatusOK, map[string]interface{}{"result": res})
}

// load reads the schedule named in the URL, writing the error response when
// it cannot.
func (h *PatchSchedulesHandler) load(w http.ResponseWriter, r *http.Request) (*patchschedule.Schedule, bool) {
	id := chi.URLParam(r, "id")
	if !isValidPatchUUID(id) {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid schedule ID"})
		return nil, false
	}
	s, err := h.store().Get(r.Context(), id)
	if errors.Is(err, patchschedule.ErrNotFound) {
		JSON(w, http.StatusNotFound, map[string]string{"error": "Patch schedule not found"})
		return nil, false
	}
	if err != nil {
		h.log.Error("patch schedules: get", "schedule_id", id, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to load the patch schedule"})
		return nil, false
	}
	return s, true
}

// respond writes the schedule id as stored, its host group name included.
func (h *PatchSchedulesHandler) respond(w http.ResponseWriter, r *http.Request, status int, id string) {
	s, err := h.store().Get(r.Context(), id)
	if err != nil {
		h.log.Error("patch schedules: read back", "schedule_id", id, "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to load the patch schedule"})
		return
	}
	JSON(w, status, s)
}
