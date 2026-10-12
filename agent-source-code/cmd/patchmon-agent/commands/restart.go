package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
	"time"

	"patchmon-agent/internal/config"
	"patchmon-agent/internal/logutil"
	"patchmon-agent/internal/packages"
	"patchmon-agent/internal/reboot"
)

// Restarting the host, after a patch run that asks for it and on the
// server's restart command. internal/reboot bounds the options and talks to
// the operating system; this file decides when.
//
// One lock serialises every restart decision with the registration of patch
// runs: a real run does not start when a restart this agent scheduled would
// cut it off, and no restart is scheduled while a run is in progress or
// another restart is pending. The agent's self-update, which restarts the
// agent, follows the same rule: it waits for runs to end, and no run starts
// while it is in progress. So does the agent restart that applies a
// configuration from the server: it waits for the runs in progress to end,
// and no run starts while it waits.

const (
	restartCommandTimeout = 30 * time.Second
	// pendingRestartGrace: a restart still "pending" this long after its
	// time did not happen (aborted on the host, or the clock moved), so it
	// no longer holds runs back.
	pendingRestartGrace = 15 * time.Minute
	// runOverrun is how far past its time limit a run can go: the last
	// Windows install still gets its minimum time, then the final status
	// and the post-patch report go out.
	runOverrun = windowsMinItemTimeout + 5*time.Minute
)

var restartGate struct {
	sync.Mutex
	pendingAt time.Time
	// agentRestartWaiting: apply_config saved a configuration and the
	// agent restarts to apply it once no patch run is in progress.
	agentRestartWaiting bool
}

// Seams for tests: the restart itself goes to the operating system.
var (
	scheduleHostRestart = func(d time.Duration, msg string) error {
		ctx, cancel := context.WithTimeout(context.Background(), restartCommandTimeout)
		defer cancel()
		return reboot.Schedule(ctx, d, msg)
	}
	cancelHostRestart = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), restartCommandTimeout)
		defer cancel()
		return reboot.Cancel(ctx)
	}
	// osPendingRestart reports a restart the operating system has
	// scheduled, whoever asked for it, including one this agent scheduled
	// before it was restarted itself.
	osPendingRestart = reboot.Pending
	// reportRunRestart tells the server about a restart a patch run
	// scheduled, or failed to schedule, for the host's activity history.
	reportRunRestart = sendRunRestartEvent
	// remoteRestartsAllowed reports the host's opt-in for the restarts
	// PatchMon asks for, set in config.yml only.
	remoteRestartsAllowed = remoteRestartsEnabledInConfig
	// restartAgentService restarts the agent's own service. It normally
	// ends this process; it returns only when the restart failed, or will
	// happen in a moment.
	restartAgentService = func() error { return restartService("", "") }
)

// pendingRestartLocked returns the restart this agent scheduled, if it is
// still to come. The caller holds restartGate.
func pendingRestartLocked(now time.Time) (time.Time, bool) {
	at := restartGate.pendingAt
	if at.IsZero() || now.After(at.Add(pendingRestartGrace)) {
		return time.Time{}, false
	}
	return at, true
}

func remoteRestartsEnabledInConfig() bool {
	return cfgManager != nil && cfgManager.IsIntegrationEnabled(config.RemoteRestartIntegration)
}

// localConfigPath is the config.yml the host's administrator edits.
func localConfigPath() string {
	if cfgManager == nil {
		return config.DefaultConfigFilePath()
	}
	return cfgManager.GetConfigFile()
}

// remoteRestartsDisabled says why a patch run's restart was skipped on a host
// that has not opted in.
func remoteRestartsDisabled() string {
	return fmt.Sprintf("remote restarts are disabled on this host (%s in %s)", config.RemoteRestartIntegration, localConfigPath())
}

func formatRestartTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 MST")
}

// patchRun is a run in progress, as registered in patchRunCancels. Entries
// are pointers so a run removes its own entry and never another one stored
// under the same id (func values are not comparable).
type patchRun struct {
	cancel context.CancelFunc
}

// errDuplicatePatchRun is returned for a run_patch whose run is already in
// progress: the server delivered it twice. It is ignored without a word to
// the server, which would otherwise record the real run as failed.
var errDuplicatePatchRun = errors.New("this patch run is already in progress on this host")

// registerPatchRun records a run so stop requests and restarts see it. A
// real run is refused when a pending restart could cut it off: one this
// agent scheduled, or one the operating system reports, before the run
// could finish, or at a time it does not tell. A dry run changes nothing
// and always starts. A duplicate of a run in progress is refused first,
// with errDuplicatePatchRun, and leaves that run's entry alone.
func registerPatchRun(id string, cancel context.CancelFunc, dryRun bool) (*patchRun, error) {
	restartGate.Lock()
	defer restartGate.Unlock()
	if _, ok := patchRunCancels.Load(id); ok {
		return nil, errDuplicatePatchRun
	}
	if updateInFlight.Load() {
		return nil, errors.New("the agent is updating itself; run the patch again in a few minutes")
	}
	if restartGate.agentRestartWaiting {
		return nil, errors.New("the agent restarts to apply a new configuration once the patch run in progress is over; run the patch again in a few minutes")
	}
	if !dryRun {
		if err := restartWouldCutRunLocked(time.Now()); err != nil {
			return nil, err
		}
	}
	run := &patchRun{cancel: cancel}
	if _, loaded := patchRunCancels.LoadOrStore(id, run); loaded {
		return nil, errDuplicatePatchRun
	}
	return run, nil
}

// unregisterPatchRun removes a run's own entry, never one a later run stored
// under the same id. The last run to end restarts the agent when a new
// configuration waits for it.
func unregisterPatchRun(id string, run *patchRun) {
	restartGate.Lock()
	patchRunCancels.CompareAndDelete(id, run)
	restartNow := restartGate.agentRestartWaiting && !otherPatchRunInProgress("")
	restartGate.Unlock()
	if restartNow {
		logger.Info("The last patch run is over; restarting the agent to apply the new configuration")
		_ = restartAgentNow()
	}
}

// restartAgentForConfig restarts the agent so the configuration apply_config
// saved takes effect. The restart would cut a patch run off (on Linux,
// systemd stops the package manager with the agent; on Windows the run's
// status is lost), so while one is in progress, dry runs included, it waits
// for the last one to end, and no run starts meanwhile.
func restartAgentForConfig() error {
	restartGate.Lock()
	if restartGate.agentRestartWaiting {
		restartGate.Unlock()
		logger.Info("Configuration saved; the agent restart to apply it already waits for the patch run in progress")
		return nil
	}
	// Set before deciding, so no run starts between now and the restart.
	restartGate.agentRestartWaiting = true
	busy := otherPatchRunInProgress("")
	restartGate.Unlock()
	if busy {
		logger.Info("Configuration saved; the agent restarts to apply it once the patch run in progress is over")
		return nil
	}
	return restartAgentNow()
}

// restartAgentNow restarts the agent for a new configuration. Runs stay
// refused unless the restart failed.
func restartAgentNow() error {
	logger.Info("Config updated, restarting patchmon-agent service...")
	if err := restartAgentService(); err != nil {
		restartGate.Lock()
		restartGate.agentRestartWaiting = false
		restartGate.Unlock()
		logger.WithError(err).Error("Could not restart the agent; the new configuration applies at its next start")
		return fmt.Errorf("restart the agent: %w", err)
	}
	return nil
}

// lookupPatchRun returns the run in progress with this id.
func lookupPatchRun(id string) (*patchRun, bool) {
	v, ok := patchRunCancels.Load(id)
	if !ok {
		return nil, false
	}
	run, ok := v.(*patchRun)
	return run, ok && run != nil
}

// beginPatchRun registers a run, or tells the server why it cannot start
// through refuse. A duplicate of a run in progress is only logged: anything
// sent for its id would land on the real run.
func beginPatchRun(id string, cancel context.CancelFunc, dryRun bool, refuse func(reason string)) (*patchRun, error) {
	run, err := registerPatchRun(id, cancel, dryRun)
	switch {
	case errors.Is(err, errDuplicatePatchRun):
		logger.WithField("patch_run_id", logutil.Sanitize(id)).Warn("Ignoring a run_patch delivered again for a run in progress")
	case err != nil:
		refuse(err.Error())
	}
	return run, err
}

func restartWouldCutRunLocked(now time.Time) error {
	finish := now.Add(patchRunTimeout() + runOverrun)
	if at, ok := pendingRestartLocked(now); ok && at.Before(finish) {
		return fmt.Errorf("a restart is scheduled for %s, before this run could finish; cancel it from the host page or run the patch after it", formatRestartTime(at))
	}
	at, ok := osPendingRestart()
	_, mine := pendingRestartLocked(now)
	switch {
	case !ok:
		return nil
	case at.IsZero() && mine:
		// The system does not tell when (SysV init, FreeBSD); the restart
		// is the one this agent scheduled, checked above.
		return nil
	case at.IsZero():
		return errors.New("a restart or shutdown is pending on this host; cancel it from the host page or run the patch after it")
	case at.Before(finish) && now.Before(at.Add(pendingRestartGrace)):
		return fmt.Errorf("a restart or shutdown is scheduled for %s, before this run could finish; cancel it from the host page or run the patch after it", formatRestartTime(at))
	}
	return nil
}

// patchRunBlocksUpdate refuses the agent's self-update while a patch run,
// dry runs included, is in progress. The caller has set updateInFlight.
func patchRunBlocksUpdate() error {
	restartGate.Lock()
	defer restartGate.Unlock()
	if otherPatchRunInProgress("") {
		return errors.New("a patch run is in progress on this host; the update waits for the next check")
	}
	return nil
}

// otherPatchRunInProgress reports whether a run other than id is registered.
func otherPatchRunInProgress(id string) bool {
	busy := false
	patchRunCancels.Range(func(k, _ any) bool {
		if k != id {
			busy = true
		}
		return !busy
	})
	return busy
}

// scheduleRestart applies a run's restart option once the run is over and
// writes what it did to out. needed says whether the host reports a pending
// restart; blocked says why the run must not restart it ("" when it may).
// It returns an error when a wanted restart could not be scheduled.
func scheduleRestart(out io.Writer, runID string, opts reboot.Options, needed bool, blocked string, schedule func(time.Duration, string) error) error {
	if !opts.Enabled() {
		return nil
	}
	if blocked != "" {
		_, _ = fmt.Fprintf(out, "Restart: not scheduled, %s\n", blocked)
		return nil
	}
	if !opts.Wanted(needed) {
		_, _ = fmt.Fprintf(out, "Restart: not needed\n")
		return nil
	}
	if !remoteRestartsAllowed() {
		_, _ = fmt.Fprintf(out, "Restart: skipped, %s\n", remoteRestartsDisabled())
		return nil
	}

	// The report goes to the server over the network: send it once the lock
	// is released (deferred first, so it runs after the unlock).
	var report func()
	defer func() {
		if report != nil {
			report()
		}
	}()
	restartGate.Lock()
	defer restartGate.Unlock()
	now := time.Now()
	if otherPatchRunInProgress(runID) {
		_, _ = fmt.Fprintf(out, "Restart: not scheduled, another patch run is in progress on this host; restart it once that run is over\n")
		return nil
	}
	if at, ok := pendingRestartLocked(now); ok {
		_, _ = fmt.Fprintf(out, "Restart: already scheduled for %s\n", formatRestartTime(at))
		return nil
	}
	at, err := opts.When(now)
	if err != nil {
		_, _ = fmt.Fprintf(out, "Restart: not scheduled, %s\n", packages.OneLine(err.Error()))
		report = func() { reportRunRestart(runID, time.Time{}, err) }
		return fmt.Errorf("restart not scheduled: %w", err)
	}
	switch err := schedule(at.Sub(now), opts.ShutdownMessage()); {
	case errors.Is(err, reboot.ErrAlreadyScheduled):
		_, _ = fmt.Fprintf(out, "Restart: not scheduled, %s (not by this run)\n", reboot.ErrAlreadyScheduled)
		return nil
	case err != nil:
		_, _ = fmt.Fprintf(out, "Restart: could not be scheduled: %s\n", packages.OneLine(err.Error()))
		report = func() { reportRunRestart(runID, time.Time{}, err) }
		return fmt.Errorf("restart could not be scheduled: %w", err)
	}
	restartGate.pendingAt = at
	_, _ = fmt.Fprintf(out, "Restart: scheduled for %s (in %s); signed-in users are warned\n",
		formatRestartTime(at), at.Sub(now).Round(time.Minute))
	report = func() { reportRunRestart(runID, at, nil) }
	return nil
}

// restartCommand schedules the restart the server asked for and returns its
// time, or why it was refused.
func restartCommand(opts reboot.Options) (time.Time, error) {
	if opts.Mode != reboot.ModeAlways {
		return time.Time{}, errors.New("invalid restart command")
	}
	if !remoteRestartsAllowed() {
		return time.Time{}, fmt.Errorf("remote restarts are disabled on this host: set \"%s: true\" under integrations in %s to allow them",
			config.RemoteRestartIntegration, localConfigPath())
	}
	restartGate.Lock()
	defer restartGate.Unlock()
	now := time.Now()
	if otherPatchRunInProgress("") {
		return time.Time{}, errors.New("a patch run is in progress on this host; try again once it is over")
	}
	if at, ok := pendingRestartLocked(now); ok {
		return time.Time{}, fmt.Errorf("a restart is already scheduled for %s; cancel it first to choose another time", formatRestartTime(at))
	}
	at, err := opts.When(now)
	if err != nil {
		return time.Time{}, err
	}
	if err := scheduleHostRestart(at.Sub(now), opts.ShutdownMessage()); err != nil {
		if errors.Is(err, reboot.ErrAlreadyScheduled) {
			return time.Time{}, fmt.Errorf("%w; cancel it first to choose another time", err)
		}
		return time.Time{}, err
	}
	restartGate.pendingAt = at
	return at, nil
}

// cancelRestartCommand cancels a pending restart, whoever scheduled it.
func cancelRestartCommand() error {
	restartGate.Lock()
	defer restartGate.Unlock()
	err := cancelHostRestart()
	if err == nil || errors.Is(err, reboot.ErrNothingScheduled) {
		restartGate.pendingAt = time.Time{}
	}
	return err
}

// requestIDPattern bounds the identifier the server attaches to a command
// so the reply can be matched to it.
var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// restartReply is the agent's answer to a restart or cancel command
// (RequestID set), or its report of a restart a patch run scheduled
// (Action "run", RunID set).
type restartReply struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	Action    string `json:"action"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	RebootAt  string `json:"reboot_at,omitempty"`
}

func sendRestartReply(r restartReply) {
	if r.RequestID == "" {
		return
	}
	writeRestartMessage(r)
}

// sendRunRestartEvent reports a patch run's restart. Best effort: without a
// connection the run's output still says what happened.
func sendRunRestartEvent(runID string, at time.Time, err error) {
	r := restartReply{RunID: runID, Action: "run"}
	if err != nil {
		r.Error = packages.OneLine(err.Error())
	} else {
		r.OK, r.RebootAt = true, at.UTC().Format(time.RFC3339)
	}
	writeRestartMessage(r)
}

func writeRestartMessage(r restartReply) {
	r.Type = "reboot_result"
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	globalWsConnMu.RLock()
	conn := globalWsConn
	globalWsConnMu.RUnlock()
	if conn == nil {
		return
	}
	if err := writeWebSocketTextMessage(conn, b); err != nil {
		logger.WithError(err).Debug("Failed to send a restart message")
	}
}

// handleRestartCommand runs the server's restart command and answers it.
func handleRestartCommand(opts reboot.Options, requestID string) {
	reply := restartReply{RequestID: requestID, Action: "reboot"}
	at, err := restartCommand(opts)
	if err != nil {
		logger.WithError(err).Warn("Restart command refused")
		reply.Error = packages.OneLine(err.Error())
	} else {
		logger.WithField("at", formatRestartTime(at)).Info("Restart scheduled on server request")
		reply.OK, reply.RebootAt = true, at.UTC().Format(time.RFC3339)
	}
	sendRestartReply(reply)
}

// handleRestartCancel runs the server's cancel command and answers it.
func handleRestartCancel(requestID string) {
	reply := restartReply{RequestID: requestID, Action: "cancel"}
	switch err := cancelRestartCommand(); {
	case errors.Is(err, reboot.ErrNothingScheduled):
		logger.Info("Restart cancel requested, but no restart was scheduled")
		reply.Error = "no restart is scheduled on this host"
	case err != nil:
		logger.WithError(err).Warn("Restart cancel failed")
		reply.Error = packages.OneLine(err.Error())
	default:
		logger.Info("Scheduled restart cancelled on server request")
		reply.OK = true
	}
	sendRestartReply(reply)
}
