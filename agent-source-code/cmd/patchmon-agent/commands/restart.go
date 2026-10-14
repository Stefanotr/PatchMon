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
//
// The restart this agent scheduled is recorded (restartrecord.go): it holds
// runs and other restarts back, and it is the only restart a cancel
// touches. A restart or shutdown scheduled outside PatchMon is left alone.

const (
	// runRestartTimeout bounds scheduling the restart of a patch run.
	runRestartTimeout = 30 * time.Second
	// ownerCheckTimeout bounds asking the operating system whose restart is
	// pending (on Windows, a System event log query) outside a command.
	ownerCheckTimeout = 10 * time.Second
	// pendingRestartGrace: a restart still "pending" this long after its
	// time did not happen (aborted on the host, or the clock moved), so it
	// no longer holds runs back.
	pendingRestartGrace = 15 * time.Minute
	// runOverrun is how far past its time limit a run can go: the last
	// Windows install still gets its minimum time, then the final status
	// and the post-patch report go out.
	runOverrun = windowsMinItemTimeout + 5*time.Minute
)

// Limits for a restart or cancel command, whose answer the server waits 20
// seconds for; variables for tests.
var (
	// commandWorkLimit bounds what the command does: waiting for the lock,
	// asking the operating system, recording the restart.
	commandWorkLimit = 12 * time.Second
	// commandReplyLimit is when the answer leaves at the latest, the work
	// done or not.
	commandReplyLimit = 15 * time.Second
)

// errRestartBusy: another restart decision held the lock for all the time a
// command has.
var errRestartBusy = errors.New("the agent is busy with another restart or patch run decision; try again in a moment")

var restartGate struct {
	sync.Mutex
	// record is the restart this agent scheduled, as last read from or
	// written to restartRecordPath (nil: none); recordLoaded says the file
	// was read.
	record       *reboot.Scheduled
	recordLoaded bool
	// agentRestartWaiting: apply_config saved a configuration and the
	// agent restarts to apply it once no patch run is in progress.
	agentRestartWaiting bool
}

// Seams for tests: the restart itself goes to the operating system.
var (
	// scheduleHostRestart schedules the restart of a patch run.
	scheduleHostRestart = func(d time.Duration, msg string) error {
		ctx, cancel := context.WithTimeout(context.Background(), runRestartTimeout)
		defer cancel()
		return reboot.Schedule(ctx, d, msg)
	}
	// scheduleCommandRestart schedules the restart of a server command,
	// within ctx.
	scheduleCommandRestart = reboot.Schedule
	// cancelHostRestart cancels the restart this agent scheduled, only if
	// it is the one pending, within ctx.
	cancelHostRestart = reboot.CancelScheduled
	// osPendingRestart reports a restart the operating system has
	// scheduled, whoever asked for it, including one this agent scheduled
	// before it was restarted itself.
	osPendingRestart = reboot.Pending
	// pendingRestartOwner says whether the restart the operating system has
	// pending is the one this agent recorded, within ctx.
	pendingRestartOwner = reboot.PendingOwner
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

// ownRestartLocked returns the restart this agent scheduled, if it may still
// be to come: recorded, its time not past by more than pendingRestartGrace,
// and not shown gone or replaced by the operating system. proven says the
// system confirmed it is the one pending (reboot.OwnerPatchMon); when the
// system cannot tell (reboot.OwnerUnknown) the record still holds runs and
// other restarts back, the safe side, but proves nothing. A record that no
// longer holds is cleared. The caller holds restartGate.
func ownRestartLocked(ctx context.Context, now time.Time) (s reboot.Scheduled, proven, ok bool) {
	s, ok = restartRecordLocked()
	if !ok {
		return reboot.Scheduled{}, false, false
	}
	if now.After(s.At.Add(pendingRestartGrace)) {
		clearRestartRecordLocked("its time has passed")
		return reboot.Scheduled{}, false, false
	}
	switch owner := pendingRestartOwner(ctx, s); owner {
	case reboot.OwnerNone:
		clearRestartRecordLocked("the host no longer has it pending")
		return reboot.Scheduled{}, false, false
	case reboot.OwnerOther:
		clearRestartRecordLocked("the restart or shutdown pending on the host was scheduled outside PatchMon")
		return reboot.Scheduled{}, false, false
	default:
		return s, owner == reboot.OwnerPatchMon, true
	}
}

// remoteRestartsEnabledInConfig reads the switch from config.yml at each
// decision: turning it on or off (by hand, or with config remote-restart)
// applies without restarting the agent. The values loaded at startup stand
// in when the file cannot be read.
func remoteRestartsEnabledInConfig() bool {
	if cfgManager == nil {
		return false
	}
	if on, ok := config.LocalSwitchOnDisk(cfgManager.GetConfigFile(), config.RemoteRestartIntegration); ok {
		return on
	}
	return cfgManager.IsIntegrationEnabled(config.RemoteRestartIntegration)
}

// remoteRestartHeader is the header in which the agent tells the server, on
// every WebSocket connection, whether this host allows remote restarts, so
// that the UI can grey the restart out where it would be refused. The agent
// still refuses them on its own (remoteRestartsAllowed).
const remoteRestartHeader = "X-PatchMon-Remote-Restart"

// remoteRestartHeaderValue is the header's value: "enabled" or "disabled",
// or "" (header left out, the server then shows no opinion) while restarts
// are disabled but a restart this agent scheduled may still be pending: the
// restart dialog must stay open to cancel it, which the agent still accepts.
func remoteRestartHeaderValue() string {
	if remoteRestartsAllowed() {
		return "enabled"
	}
	if ownRestartMayBePending(time.Now()) {
		return ""
	}
	return "disabled"
}

// ownRestartMayBePending reports whether a restart this agent scheduled is
// recorded and its time not long past. It does not ask the operating system,
// which can take seconds a connection should not wait, and it answers yes
// when the lock is busy: a wrong yes only leaves the restart button active
// (the agent still refuses), a wrong no would hide the cancel.
func ownRestartMayBePending(now time.Time) bool {
	if !restartGate.TryLock() {
		return true
	}
	defer restartGate.Unlock()
	s, ok := restartRecordLocked()
	return ok && !now.After(s.At.Add(pendingRestartGrace))
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
		ctx, cancel := context.WithTimeout(context.Background(), ownerCheckTimeout)
		defer cancel()
		if err := restartWouldCutRunLocked(ctx, time.Now()); err != nil {
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

func restartWouldCutRunLocked(ctx context.Context, now time.Time) error {
	finish := now.Add(patchRunTimeout() + runOverrun)
	own, proven, mine := ownRestartLocked(ctx, now)
	if mine && own.At.Before(finish) {
		return fmt.Errorf("a restart is scheduled for %s, before this run could finish; cancel it from the host page or run the patch after it", formatRestartTime(own.At))
	}
	at, ok := osPendingRestart()
	switch {
	case !ok:
		return nil
	case at.IsZero() && proven:
		// The system does not tell when (SysV init, FreeBSD); the restart
		// is, as it confirmed, the one this agent scheduled, checked above.
		// Unconfirmed (another shutdown pending as well), it could be
		// sooner.
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

// skippedRestart is a restart a run wanted but left out, and why. It is
// reported to the server as skipped and does not fail the run, whose output
// says what happened.
type skippedRestart struct{ reason string }

func (s skippedRestart) Error() string { return s.reason }

// scheduleRestart applies a run's restart option once the run is over and
// writes what it did to out. needed says whether the host reports a pending
// restart; blocked says why the run must not restart it ("" when it may).
// A restart left out, because the run was stopped or failed, another run is
// in progress, a restart or shutdown is already pending, or the host's opt-in
// is off, is reported as skipped. It returns
// an error when a wanted restart could not be scheduled.
func scheduleRestart(out io.Writer, runID string, opts reboot.Options, needed bool, blocked string, schedule func(time.Duration, string) error) error {
	if !opts.Enabled() {
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
	skip := func(line, reason string) {
		_, _ = fmt.Fprintln(out, line)
		report = func() { reportRunRestart(runID, time.Time{}, skippedRestart{reason}) }
	}
	if blocked != "" {
		// A stopped or failed run: the restart it asked for is reported as
		// skipped, so the restart history says why the host did not restart.
		skip("Restart: not scheduled, "+blocked, blocked)
		return nil
	}
	if !opts.Wanted(needed) {
		_, _ = fmt.Fprintf(out, "Restart: not needed\n")
		return nil
	}
	if !remoteRestartsAllowed() {
		reason := remoteRestartsDisabled()
		skip("Restart: skipped, "+reason, reason)
		return nil
	}
	restartGate.Lock()
	defer restartGate.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), ownerCheckTimeout)
	defer cancel()
	now := time.Now()
	if otherPatchRunInProgress(runID) {
		skip("Restart: not scheduled, another patch run is in progress on this host; restart it once that run is over",
			"another patch run is in progress on this host")
		return nil
	}
	if own, _, ok := ownRestartLocked(ctx, now); ok {
		skip("Restart: already scheduled for "+formatRestartTime(own.At), "a restart is already scheduled for "+formatRestartTime(own.At))
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
		skip(fmt.Sprintf("Restart: not scheduled, %s (not by this run)", reboot.ErrAlreadyScheduled), reboot.ErrAlreadyScheduled.Error())
		return nil
	case err != nil:
		_, _ = fmt.Fprintf(out, "Restart: could not be scheduled: %s\n", packages.OneLine(err.Error()))
		report = func() { reportRunRestart(runID, time.Time{}, err) }
		return fmt.Errorf("restart could not be scheduled: %w", err)
	}
	setRestartRecordLocked(reboot.Scheduled{At: at, Since: now})
	_, _ = fmt.Fprintf(out, "Restart: scheduled for %s (in %s); signed-in users are warned\n",
		formatRestartTime(at), at.Sub(now).Round(time.Minute))
	report = func() { reportRunRestart(runID, at, nil) }
	return nil
}

// lockRestartGate takes restartGate, waiting no longer than ctx allows.
func lockRestartGate(ctx context.Context) error {
	for !restartGate.TryLock() {
		select {
		case <-ctx.Done():
			return errRestartBusy
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil
}

// restartCommand schedules the restart the server asked for and returns its
// time, or why it was refused, within ctx.
func restartCommand(ctx context.Context, opts reboot.Options) (time.Time, error) {
	if opts.Mode != reboot.ModeAlways {
		return time.Time{}, errors.New("invalid restart command")
	}
	if !remoteRestartsAllowed() {
		return time.Time{}, fmt.Errorf("remote restarts are disabled on this host: to allow them, run \"patchmon-agent config remote-restart on\" on the host, or set \"%s: true\" under integrations in %s",
			config.RemoteRestartIntegration, localConfigPath())
	}
	if err := lockRestartGate(ctx); err != nil {
		return time.Time{}, err
	}
	defer restartGate.Unlock()
	now := time.Now()
	if otherPatchRunInProgress("") {
		return time.Time{}, errors.New("a patch run is in progress on this host; try again once it is over")
	}
	if own, _, ok := ownRestartLocked(ctx, now); ok {
		return time.Time{}, fmt.Errorf("a restart is already scheduled for %s; cancel it first to choose another time", formatRestartTime(own.At))
	}
	at, err := opts.When(now)
	if err != nil {
		return time.Time{}, err
	}
	if err := scheduleCommandRestart(ctx, at.Sub(now), opts.ShutdownMessage()); err != nil {
		if errors.Is(err, reboot.ErrAlreadyScheduled) {
			return time.Time{}, fmt.Errorf("%w; cancel it first to choose another time", err)
		}
		return time.Time{}, err
	}
	setRestartRecordLocked(reboot.Scheduled{At: at, Since: now})
	return at, nil
}

// cancelRestartCommand cancels the restart this agent scheduled, and nothing
// else: without a record of one still pending, it refuses with
// reboot.ErrNotScheduledByPatchMon. It works within ctx.
func cancelRestartCommand(ctx context.Context) error {
	if err := lockRestartGate(ctx); err != nil {
		return err
	}
	defer restartGate.Unlock()
	// The host checks again, and refuses unless it confirms the restart is
	// PatchMon's.
	own, _, ok := ownRestartLocked(ctx, time.Now())
	if !ok {
		return reboot.ErrNotScheduledByPatchMon
	}
	switch err := cancelHostRestart(ctx, own); {
	case err == nil:
		clearRestartRecordLocked("cancelled on server request")
		return nil
	case errors.Is(err, reboot.ErrNothingScheduled):
		clearRestartRecordLocked("the host no longer has it pending")
		return err
	default:
		return err
	}
}

// requestIDPattern bounds the identifier the server attaches to a command
// so the reply can be matched to it.
var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// restartReply is the agent's answer to a restart or cancel command
// (RequestID set), or its report of the restart of a patch run (Action
// "run", RunID set): scheduled (OK), skipped (Skipped, Error says why) or
// failed (Error).
type restartReply struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id,omitempty"`
	Action    string `json:"action"`
	RunID     string `json:"run_id,omitempty"`
	OK        bool   `json:"ok"`
	Skipped   bool   `json:"skipped,omitempty"`
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
	writeRestartMessage(runRestartReply(runID, at, err))
}

// runRestartReply is the report of a run's restart: scheduled for at when
// err is nil, skipped when err is a skippedRestart, failed otherwise.
func runRestartReply(runID string, at time.Time, err error) restartReply {
	r := restartReply{RunID: runID, Action: "run"}
	var skipped skippedRestart
	switch {
	case errors.As(err, &skipped):
		r.Skipped, r.Error = true, packages.OneLine(skipped.reason)
	case err != nil:
		r.Error = packages.OneLine(err.Error())
	default:
		r.OK, r.RebootAt = true, at.UTC().Format(time.RFC3339)
	}
	return r
}

func encodeRestartMessage(r restartReply) ([]byte, error) {
	r.Type = "reboot_result"
	return json.Marshal(r)
}

func writeRestartMessage(r restartReply) {
	b, err := encodeRestartMessage(r)
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

// answerCommand runs a restart or cancel command and returns its answer
// within commandReplyLimit, whatever happens: the work has commandWorkLimit,
// and should something not honour it, an answer still goes out at the limit
// (the work carries on and logs its outcome).
func answerCommand(action, late string, work func(context.Context) restartReply) restartReply {
	ctx, cancel := context.WithTimeout(context.Background(), commandWorkLimit)
	done := make(chan restartReply, 1)
	go func() {
		defer cancel()
		done <- work(ctx)
	}()
	timer := time.NewTimer(commandReplyLimit)
	defer timer.Stop()
	select {
	case r := <-done:
		return r
	case <-timer.C:
		logger.WithField("action", action).Warn("Restart command still running at its time limit; answering the server now")
		return restartReply{Action: action, Error: late}
	}
}

// restartCommandReply runs the server's restart command.
func restartCommandReply(opts reboot.Options) restartReply {
	late := "the operating system did not answer in time; the restart may still be scheduled, check the host"
	return answerCommand("reboot", late, func(ctx context.Context) restartReply {
		reply := restartReply{Action: "reboot"}
		at, err := restartCommand(ctx, opts)
		if err != nil {
			logger.WithError(err).Warn("Restart command refused")
			reply.Error = packages.OneLine(err.Error())
		} else {
			logger.WithField("at", formatRestartTime(at)).Info("Restart scheduled on server request")
			reply.OK, reply.RebootAt = true, at.UTC().Format(time.RFC3339)
		}
		return reply
	})
}

// cancelCommandReply runs the server's cancel command.
func cancelCommandReply() restartReply {
	late := "the operating system did not answer in time; the restart may still be pending, check the host"
	return answerCommand("cancel", late, func(ctx context.Context) restartReply {
		reply := restartReply{Action: "cancel"}
		switch err := cancelRestartCommand(ctx); {
		case errors.Is(err, reboot.ErrNothingScheduled):
			logger.Info("Restart cancel requested, but no restart was scheduled")
			reply.Error = "no restart is scheduled on this host"
		case errors.Is(err, reboot.ErrNotScheduledByPatchMon):
			logger.Info("Restart cancel requested, but no restart scheduled by PatchMon is pending; anything else is left alone")
			reply.Error = err.Error()
		case errors.Is(err, reboot.ErrOwnerUnconfirmed):
			logger.WithError(err).Warn("Restart cancel refused: the pending restart is not confirmed as PatchMon's")
			reply.Error = packages.OneLine(err.Error())
		case err != nil:
			logger.WithError(err).Warn("Restart cancel failed")
			reply.Error = packages.OneLine(err.Error())
		default:
			logger.Info("Scheduled restart cancelled on server request")
			reply.OK = true
		}
		return reply
	})
}

// Seams for tests: the command handlers started by handleRestartMessage.
var (
	startRestartCommand = handleRestartCommand
	startRestartCancel  = handleRestartCancel
)

// handleRestartMessage takes a "reboot" or "reboot_cancel" message from the
// WebSocket reader and starts its handler at once. It does not go through
// the service loop, which runs check-ins, reports, agent updates and
// apply_config inline: the answer could miss the 20 seconds the server
// waits, or the command be dropped while the loop's buffer is full. The
// handlers are serialised by restartGate and bounded in time.
func handleRestartMessage(msgType, requestID string, opts *reboot.Options) {
	if requestID != "" && !requestIDPattern.MatchString(requestID) {
		logger.Warn("Ignoring restart command with an invalid request_id")
		return
	}
	if msgType == "reboot_cancel" {
		logger.Info("reboot_cancel received")
		go startRestartCancel(requestID)
		return
	}
	var restart reboot.Options
	if opts != nil {
		restart = *opts
	}
	if err := restart.Validate(); err != nil {
		logger.WithError(err).Warn("Ignoring invalid reboot command")
		go sendRestartReply(restartReply{RequestID: requestID, Action: "reboot", Error: "invalid restart options"})
		return
	}
	logger.Info("reboot received")
	go startRestartCommand(restart, requestID)
}

// handleRestartCommand runs the server's restart command and answers it.
func handleRestartCommand(opts reboot.Options, requestID string) {
	reply := restartCommandReply(opts)
	reply.RequestID = requestID
	sendRestartReply(reply)
}

// handleRestartCancel runs the server's cancel command and answers it.
func handleRestartCancel(requestID string) {
	reply := cancelCommandReply()
	reply.RequestID = requestID
	sendRestartReply(reply)
}
