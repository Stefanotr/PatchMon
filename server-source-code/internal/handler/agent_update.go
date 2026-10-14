package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

// How an agent update goes, as the agent tells it: an update_progress
// message on its WebSocket for each stage, and its version on every
// connection, which ends the update once the agent is back on another one.
// Older agents send neither: their update shows as sent until the new
// version connects, or until the patch-run cleanup closes it.

// AgentVersionHeader is the header in which an agent tells its version on
// connecting.
const AgentVersionHeader = "X-PatchMon-Agent-Version"

// agentVersionRe bounds what an agent may send as a version.
var agentVersionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)

// ParseAgentVersion returns the version without a leading "v", or "" when
// the agent sent none, or something that is not a version.
func ParseAgentVersion(v string) string {
	v = store.NormalizeAgentVersion(v)
	if !agentVersionRe.MatchString(v) {
		return ""
	}
	return v
}

// Bounds of the text an agent sends with a stage.
const updateProgressMessageMax = 300

// ParseUpdateProgress reads an update_progress message. ok is false for an
// unknown stage; what else does not hold together is dropped.
func ParseUpdateProgress(raw []byte) (p store.AgentUpdateProgress, ok bool) {
	var m struct {
		Stage       string   `json:"stage"`
		Percent     *float64 `json:"percent"`
		FromVersion string   `json:"from_version"`
		ToVersion   string   `json:"to_version"`
		Message     string   `json:"message"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || !store.IsAgentReportedUpdateStage(m.Stage) {
		return store.AgentUpdateProgress{}, false
	}
	p = store.AgentUpdateProgress{
		Stage:       m.Stage,
		FromVersion: ParseAgentVersion(m.FromVersion),
		ToVersion:   ParseAgentVersion(m.ToVersion),
		Message:     agentText(m.Message, updateProgressMessageMax),
	}
	if m.Stage == store.AgentUpdateDownloading && m.Percent != nil {
		pct := int(*m.Percent)
		pct = max(0, min(100, pct))
		p.Percent = &pct
	}
	return p, true
}

// agentText makes text an agent sent fit for display: one line, no control
// characters, at most limit runes.
func agentText(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		s = string(r[:limit]) + "..."
	}
	return s
}

// OnAgentUpdateProgress receives a stage an agent reported.
type OnAgentUpdateProgress func(ctx context.Context, apiID, hostID string, p store.AgentUpdateProgress)

// OnAgentVersion receives the version of a connecting agent ("" when it
// said none).
type OnAgentVersion func(ctx context.Context, apiID, version string)

// WithOnAgentUpdateProgress sets the callback for update_progress messages.
func WithOnAgentUpdateProgress(f OnAgentUpdateProgress) AgentWSHandlerOption {
	return func(h *AgentWSHandler) {
		h.onUpdateProgress = f
	}
}

// WithOnAgentVersion sets the callback for the version of each connecting
// agent.
func WithOnAgentVersion(f OnAgentVersion) AgentWSHandlerOption {
	return func(h *AgentWSHandler) {
		h.onAgentVersion = f
	}
}

type agentUpdateRecorder interface {
	RecordProgress(ctx context.Context, apiID, hostID string, p store.AgentUpdateProgress) error
	RecordConnected(ctx context.Context, apiID, version string) error
}

// agentUpdateRecordTimeout bounds each write.
const agentUpdateRecordTimeout = 5 * time.Second

// RecordAgentUpdateProgress stores each stage. A failure is logged: the
// update itself goes on.
func RecordAgentUpdateProgress(s agentUpdateRecorder) OnAgentUpdateProgress {
	return func(ctx context.Context, apiID, hostID string, p store.AgentUpdateProgress) {
		ctx, cancel := context.WithTimeout(ctx, agentUpdateRecordTimeout)
		defer cancel()
		if err := s.RecordProgress(ctx, apiID, hostID, p); err != nil {
			slog.Warn("agent update stage not recorded", "api_id", apiID, "stage", p.Stage, "error", err)
		}
	}
}

// RecordAgentVersion closes the update an agent connecting on a new version
// finished.
func RecordAgentVersion(s agentUpdateRecorder) OnAgentVersion {
	return func(ctx context.Context, apiID, version string) {
		if version == "" {
			return
		}
		ctx, cancel := context.WithTimeout(ctx, agentUpdateRecordTimeout)
		defer cancel()
		if err := s.RecordConnected(ctx, apiID, version); err != nil {
			slog.Warn("agent update end not recorded", "api_id", apiID, "error", err)
		}
	}
}

// Flow control of one connection's update_progress messages: a backlog
// beyond updateProgressBacklog is dropped, download percentages closer than
// updateProgressMinGap to the last one recorded are skipped, and records are
// rationed: a burst of updateProgressBurst, then one per
// updateProgressRefill. An agent sends a couple of dozen per update.
const (
	updateProgressBacklog = 32
	updateProgressMinGap  = 500 * time.Millisecond
	updateProgressBurst   = 40
	updateProgressRefill  = time.Second
)

// updateProgressWorker records one connection's update stages in the order
// they came, off the connection's read loop.
type updateProgressWorker struct {
	ch   chan store.AgentUpdateProgress
	done chan struct{}
}

// startUpdateProgressWorker starts the worker. ctx keeps its values (the
// host's database) but not its end: the last stage before an agent restarts
// arrives just before its connection closes.
func startUpdateProgressWorker(ctx context.Context, apiID, hostID string, record OnAgentUpdateProgress) *updateProgressWorker {
	w := &updateProgressWorker{ch: make(chan store.AgentUpdateProgress, updateProgressBacklog), done: make(chan struct{})}
	ctx = context.WithoutCancel(ctx)
	go func() {
		defer close(w.done)
		var last store.AgentUpdateProgress
		var lastAt time.Time
		tokens, refilled := float64(updateProgressBurst), time.Now()
		for p := range w.ch {
			now := time.Now()
			tokens = min(updateProgressBurst, tokens+float64(now.Sub(refilled))/float64(updateProgressRefill))
			refilled = now
			if skipUpdateProgress(last, lastAt, p, now) || tokens < 1 {
				continue
			}
			tokens--
			last, lastAt = p, now
			record(ctx, apiID, hostID, p)
		}
	}()
	return w
}

// skipUpdateProgress: another download percentage too soon after the last
// one recorded. Stage changes and the end of the download always go through.
func skipUpdateProgress(last store.AgentUpdateProgress, lastAt time.Time, p store.AgentUpdateProgress, now time.Time) bool {
	if p.Stage != store.AgentUpdateDownloading || last.Stage != store.AgentUpdateDownloading {
		return false
	}
	if p.Percent != nil && *p.Percent == 100 {
		return false
	}
	return now.Sub(lastAt) < updateProgressMinGap
}

// offer queues a message, read first so the backlog holds no raw message,
// or drops it when it is not a stage or the backlog is full.
func (w *updateProgressWorker) offer(raw []byte) {
	p, ok := ParseUpdateProgress(raw)
	if !ok {
		return
	}
	select {
	case w.ch <- p:
	default:
	}
}

// stop lets the worker record what is queued, then end.
func (w *updateProgressWorker) stop() {
	close(w.ch)
}
