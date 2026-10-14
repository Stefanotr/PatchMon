package handler

import (
	"encoding/json"
	"sync"
	"time"
)

// The compliance_scan_progress messages agents send while they scan, kept in
// memory (the latest per agent) for the live part of the host's activity.
// The agent connections live in this process too (agentregistry).

// Phases an agent reports, and whether they end the scan.
var compliancePhaseEnds = map[string]bool{
	"started":    false,
	"evaluating": false,
	"parsing":    false,
	"sending":    false,
	"completed":  true,
	"failed":     true,
	"cancelled":  true,
}

const (
	// complianceProgressStale: a scan in progress with no news for this
	// long is no longer shown (agent gone mid-scan). An OpenSCAP evaluation
	// sends nothing for minutes.
	complianceProgressStale = 30 * time.Minute
	// liveFinishedWindow: how long a task that ended stays in the live view.
	liveFinishedWindow = 90 * time.Second
	// complianceProgressMax bounds the agents kept, should many scan at once.
	complianceProgressMax = 10000
)

// ComplianceProgress is the last progress an agent reported for a scan.
type ComplianceProgress struct {
	Phase       string
	ProfileName string
	Message     string
	Error       string
	Percent     int
	StartedAt   time.Time
	UpdatedAt   time.Time
}

// Finished reports whether the scan ended.
func (p ComplianceProgress) Finished() bool { return compliancePhaseEnds[p.Phase] }

// ComplianceProgressTracker keeps the latest progress of each agent.
type ComplianceProgressTracker struct {
	mu    sync.Mutex
	byKey map[string]ComplianceProgress
	now   func() time.Time
}

// NewComplianceProgressTracker creates the tracker.
func NewComplianceProgressTracker() *ComplianceProgressTracker {
	return &ComplianceProgressTracker{byKey: map[string]ComplianceProgress{}, now: time.Now}
}

// Record keeps a compliance_scan_progress message of the agent key (its
// context and api_id, from the authenticated connection). Unknown phases are
// ignored.
func (t *ComplianceProgressTracker) Record(key string, raw []byte) {
	var m struct {
		Phase       string  `json:"phase"`
		ProfileName string  `json:"profile_name"`
		Message     string  `json:"message"`
		Progress    float64 `json:"progress"`
		Error       string  `json:"error"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return
	}
	if _, known := compliancePhaseEnds[m.Phase]; !known {
		return
	}
	now := t.now()
	p := ComplianceProgress{
		Phase:       m.Phase,
		ProfileName: agentText(m.ProfileName, 120),
		Message:     agentText(m.Message, 200),
		Error:       agentText(m.Error, 300),
		Percent:     max(0, min(100, int(m.Progress))),
		StartedAt:   now,
		UpdatedAt:   now,
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.byKey[key]; ok && t.freshLocked(prev, now) && !prev.Finished() &&
		m.Phase != "started" && prev.ProfileName == p.ProfileName {
		p.StartedAt = prev.StartedAt
	}
	t.byKey[key] = p
	if len(t.byKey) > complianceProgressMax {
		t.pruneLocked(now)
	}
}

// Get returns the agent's scan in progress, or one that ended a moment ago.
func (t *ComplianceProgressTracker) Get(key string) (ComplianceProgress, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.byKey[key]
	if !ok {
		return ComplianceProgress{}, false
	}
	if !t.freshLocked(p, t.now()) {
		delete(t.byKey, key)
		return ComplianceProgress{}, false
	}
	return p, true
}

func (t *ComplianceProgressTracker) freshLocked(p ComplianceProgress, now time.Time) bool {
	if p.Finished() {
		return now.Sub(p.UpdatedAt) <= liveFinishedWindow
	}
	return now.Sub(p.UpdatedAt) <= complianceProgressStale
}

func (t *ComplianceProgressTracker) pruneLocked(now time.Time) {
	for id, p := range t.byKey {
		if !t.freshLocked(p, now) {
			delete(t.byKey, id)
		}
	}
}
