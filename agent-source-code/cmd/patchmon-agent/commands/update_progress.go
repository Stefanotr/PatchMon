package commands

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
)

// How an agent update goes, told to the server over the WebSocket so the
// host's activity shows it live: the server records each stage on the
// update's row and closes it once the agent reconnects on the new version
// (agentVersionHeader).

// updateProgressType is the WebSocket message type.
const updateProgressType = "update_progress"

// Stages of an agent update, in order. upToDate, skipped and failed end it
// on the agent's side; restarting is the last one the old process reports.
const (
	updateStageChecking    = "checking"
	updateStageUpToDate    = "up_to_date"
	updateStageDownloading = "downloading"
	updateStageVerifying   = "verifying"
	updateStageInstalling  = "installing"
	updateStageRestarting  = "restarting"
	updateStageSkipped     = "skipped"
	updateStageFailed      = "failed"
)

// agentVersionHeader tells the server, on every WebSocket connection, which
// version the agent runs: an update the server is following ends when the
// agent comes back on another version.
const agentVersionHeader = "X-PatchMon-Agent-Version"

// updateMessageMax bounds the message (an error) sent with a stage.
const updateMessageMax = 300

// updateOrigin is what started an update.
type updateOrigin int

const (
	// updateFromServer: the server's update_agent command. Every stage is
	// reported, the checks that end it early included.
	updateFromServer updateOrigin = iota
	// updateAutomatic: the check after a report. Only an update that gets
	// as far as downloading is reported, or the check after every report
	// would show in the activity.
	updateAutomatic
	// updateManual: the update-agent command, run on the host. It has no
	// WebSocket, so nothing is reported.
	updateManual
)

// updateSkippedError is an update the agent did not start, and why (a patch
// run in progress, a recent update).
type updateSkippedError struct{ err error }

func (e updateSkippedError) Error() string { return "update skipped: " + e.err.Error() }
func (e updateSkippedError) Unwrap() error { return e.err }

type updateProgressMessage struct {
	Type        string `json:"type"`
	Stage       string `json:"stage"`
	Percent     *int   `json:"percent,omitempty"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version,omitempty"`
	Message     string `json:"message,omitempty"`
}

// sendServerMessage writes a message to the server, if the agent is connected.
// It is a variable for tests.
var sendServerMessage = func(b []byte) error {
	globalWsConnMu.RLock()
	conn := globalWsConn
	globalWsConnMu.RUnlock()
	if conn == nil {
		return errors.New("not connected")
	}
	return writeWebSocketTextMessage(conn, b)
}

// updateReporter reports the stages of one update.
type updateReporter struct {
	mu          sync.Mutex
	origin      updateOrigin
	from, to    string
	started     bool // downloading reached: an automatic update reports from here
	lastPercent int
}

func newUpdateReporter(origin updateOrigin, from string) *updateReporter {
	return &updateReporter{origin: origin, from: from, lastPercent: -1}
}

// setTarget records the version the update installs.
func (u *updateReporter) setTarget(to string) {
	u.mu.Lock()
	u.to = to
	u.mu.Unlock()
}

// stage reports a stage, with an optional message.
func (u *updateReporter) stage(stage, message string) {
	u.mu.Lock()
	if stage == updateStageDownloading {
		u.started = true
	}
	quiet := u.quietLocked()
	m := u.messageLocked(stage, message)
	u.mu.Unlock()
	if !quiet {
		u.send(m)
	}
}

// percent reports how far the download got, every 10 percent.
func (u *updateReporter) percent(p int) {
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	u.mu.Lock()
	if p == u.lastPercent || (u.lastPercent >= 0 && p != 100 && p < u.lastPercent+10) {
		u.mu.Unlock()
		return
	}
	u.lastPercent = p
	quiet := u.quietLocked()
	m := u.messageLocked(updateStageDownloading, "")
	m.Percent = &p
	u.mu.Unlock()
	if !quiet {
		u.send(m)
	}
}

// fail reports how the update ended when updateAgent returned err: skipped
// or failed.
func (u *updateReporter) fail(err error) {
	if err == nil {
		return
	}
	var skipped updateSkippedError
	if errors.As(err, &skipped) {
		u.stage(updateStageSkipped, skipped.err.Error())
		return
	}
	u.stage(updateStageFailed, err.Error())
}

func (u *updateReporter) quietLocked() bool {
	switch u.origin {
	case updateManual:
		return true
	case updateAutomatic:
		return !u.started
	default:
		return false
	}
}

func (u *updateReporter) messageLocked(stage, message string) updateProgressMessage {
	// oneLine names an empty text "(unnamed)": a stage has no message.
	if message != "" {
		message = oneLine(message)
	}
	if len(message) > updateMessageMax {
		message = strings.ToValidUTF8(message[:updateMessageMax], "")
	}
	return updateProgressMessage{Type: updateProgressType, Stage: stage, FromVersion: u.from, ToVersion: u.to, Message: message}
}

func (u *updateReporter) send(m updateProgressMessage) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	if err := sendServerMessage(b); err != nil {
		logger.WithError(err).Debug("Update progress not sent")
	}
}

// progressReader counts what is read and reports it against total (bytes,
// 0 when not known: nothing is reported then).
type progressReader struct {
	r      io.Reader
	total  int64
	read   int64
	report func(percent int)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.total > 0 && p.report != nil {
		p.read += int64(n)
		p.report(int(p.read * 100 / p.total))
	}
	return n, err
}
