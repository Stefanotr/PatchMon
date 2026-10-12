package handler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/models"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

func newTestReplyBroker() *agentReplyBroker {
	return &agentReplyBroker{waiting: make(map[string]pendingReply)}
}

func TestAgentReplyDeliveredToTheWaitingCommand(t *testing.T) {
	b := newTestReplyBroker()
	ch, done := b.expect("req-1", "api-a")
	defer done()

	msg := []byte(`{"type":"reboot_result","request_id":"req-1","ok":true}`)
	if !b.deliver("api-a", msg) {
		t.Fatal("deliver = false, want true")
	}
	select {
	case got := <-ch:
		if string(got) != string(msg) {
			t.Fatalf("got %s, want %s", got, msg)
		}
	default:
		t.Fatal("no reply on the channel")
	}
}

func TestAgentReplyFromAnotherAgentIsRejected(t *testing.T) {
	b := newTestReplyBroker()
	ch, done := b.expect("req-1", "api-a")
	defer done()

	if b.deliver("api-b", []byte(`{"request_id":"req-1","ok":true}`)) {
		t.Fatal("a reply from another agent was accepted")
	}
	select {
	case <-ch:
		t.Fatal("a reply from another agent reached the command")
	default:
	}
	// The rightful agent can still answer.
	if !b.deliver("api-a", []byte(`{"request_id":"req-1","ok":false}`)) {
		t.Fatal("the rightful reply was rejected after a spoofed one")
	}
}

func TestAgentReplyIsAcceptedOnce(t *testing.T) {
	b := newTestReplyBroker()
	_, done := b.expect("req-1", "api-a")
	defer done()

	msg := []byte(`{"request_id":"req-1","ok":true}`)
	if !b.deliver("api-a", msg) {
		t.Fatal("first reply rejected")
	}
	if b.deliver("api-a", msg) {
		t.Fatal("second reply accepted")
	}
}

func TestAgentReplyUnknownOrMalformed(t *testing.T) {
	b := newTestReplyBroker()
	_, done := b.expect("req-1", "api-a")
	done() // no longer awaited

	for _, msg := range []string{
		`{"request_id":"req-1","ok":true}`, // forgotten
		`{"request_id":"other","ok":true}`, // never sent
		`{"ok":true}`,                      // no request id
		`not json`,
	} {
		if b.deliver("api-a", []byte(msg)) {
			t.Errorf("deliver(%s) = true, want false", msg)
		}
	}
}

func TestClipReason(t *testing.T) {
	if got := clipReason(""); got != "no reason given" {
		t.Errorf("empty reason = %q", got)
	}
	long := strings.Repeat("e", 299) + "éé" // straddles the limit
	got := clipReason(long)
	if len(got) > 300 || !strings.HasPrefix(long, got) {
		t.Errorf("clipReason kept %d bytes", len(got))
	}
	if got != strings.Repeat("e", 299) {
		t.Errorf("clipReason split a rune: %q", got[len(got)-3:])
	}
}

func TestParseRunRestart(t *testing.T) {
	runID, failure, out, ok := parseRunRestart([]byte(`{"type":"reboot_result","action":"run","run_id":"0f9c-11","ok":true,"reboot_at":"2026-10-12T23:00:00+02:00"}`))
	if !ok || runID != "0f9c-11" || failure != "" || out["reboot_at"] != "2026-10-12T21:00:00Z" || out["source"] != "patch_run" || out["patch_run_id"] != "0f9c-11" {
		t.Fatalf("scheduled: %q %q %v %v", runID, failure, out, ok)
	}

	_, failure, out, ok = parseRunRestart([]byte(`{"action":"run","run_id":"r1","ok":false,"error":"` + strings.Repeat("x", 400) + `"}`))
	if !ok || !strings.HasPrefix(failure, "the patch run could not schedule the restart: ") || len(failure) > 360 {
		t.Fatalf("failed: %q %v", failure, ok)
	}
	if _, has := out["reboot_at"]; has {
		t.Fatal("a failed restart reported a time")
	}

	_, _, out, ok = parseRunRestart([]byte(`{"action":"run","run_id":"r1","ok":true,"reboot_at":"soon"}`))
	if _, has := out["reboot_at"]; !ok || has {
		t.Fatalf("an unreadable time was kept: %v", out)
	}

	for _, msg := range []string{
		`{"action":"reboot","request_id":"x","ok":true}`, // a late command reply
		`{"action":"run","run_id":"../etc","ok":true}`,
		`{"action":"run","ok":true}`,
		`not json`,
	} {
		if _, _, _, ok := parseRunRestart([]byte(msg)); ok {
			t.Errorf("%s: accepted", msg)
		}
	}
}

func TestParseRunRestartSkipped(t *testing.T) {
	runID, failure, out, ok := parseRunRestart([]byte(`{"type":"reboot_result","action":"run","run_id":"r1","ok":false,"skipped":true,"error":"remote restarts are disabled on this host"}`))
	if !ok || runID != "r1" || failure != "restart skipped: remote restarts are disabled on this host" || out["source"] != "patch_run" {
		t.Fatalf("skipped: %q %q %v %v", runID, failure, out, ok)
	}

	// Clipped like any agent text.
	_, failure, _, _ = parseRunRestart([]byte(`{"action":"run","run_id":"r1","skipped":true,"error":"` + strings.Repeat("y", 400) + `"}`))
	if failure != "restart skipped: "+strings.Repeat("y", 300) {
		t.Fatalf("not clipped: %d bytes", len(failure))
	}

	_, failure, _, _ = parseRunRestart([]byte(`{"action":"run","run_id":"r1","skipped":true}`))
	if failure != "restart skipped: no reason given" {
		t.Fatalf("no reason: %q", failure)
	}

	// A contradictory event never records a restart as scheduled.
	_, failure, out, _ = parseRunRestart([]byte(`{"action":"run","run_id":"r1","ok":true,"skipped":true,"error":"a restart is already pending","reboot_at":"2026-10-12T23:00:00Z"}`))
	if !strings.HasPrefix(failure, "restart skipped: ") {
		t.Fatalf("ok and skipped: %q", failure)
	}
	if _, has := out["reboot_at"]; has {
		t.Fatal("a skipped restart reported a time")
	}

	// Without skipped, a failure keeps its message.
	_, failure, _, _ = parseRunRestart([]byte(`{"action":"run","run_id":"r1","ok":false,"skipped":false,"error":"shutdown failed"}`))
	if failure != "the patch run could not schedule the restart: shutdown failed" {
		t.Fatalf("failure: %q", failure)
	}
}

const testRequestID = "0b7c6a4e-3f1d-4c2b-9a8e-5d6f7a8b9c0d"

func TestCommandReplyOutcome(t *testing.T) {
	o := commandReplyOutcome([]byte(`{"type":"reboot_result","request_id":"x","action":"reboot","ok":true,"reboot_at":"2026-10-12T23:00:00+02:00"}`))
	if o.status != store.JobCompleted || o.message != "" || o.rebootAt != "2026-10-12T21:00:00Z" {
		t.Fatalf("done: %+v", o)
	}
	o = commandReplyOutcome([]byte(`{"ok":false,"error":"remote restarts are disabled on this host: set allow_remote_reboot"}`))
	if o.status != store.JobFailed || o.message != "refused by the agent: remote restarts are disabled on this host: set allow_remote_reboot" ||
		o.refusal != "remote restarts are disabled on this host: set allow_remote_reboot" {
		t.Fatalf("refused: %+v", o)
	}
	o = commandReplyOutcome([]byte(`{"ok":"yes"}`))
	if o.status != store.JobFailed || !o.unreadable || o.message != "unreadable answer from the agent" {
		t.Fatalf("unreadable: %+v", o)
	}
}

func TestParseLateCommandReply(t *testing.T) {
	id, o, ok := parseLateCommandReply([]byte(`{"type":"reboot_result","request_id":"` + testRequestID + `","action":"cancel","ok":false,"error":"no restart is scheduled on this host"}`))
	if !ok || id != testRequestID || o.status != store.JobFailed || o.message != "refused by the agent: no restart is scheduled on this host" {
		t.Fatalf("late refusal: %q %+v %v", id, o, ok)
	}
	for _, msg := range []string{
		`{"action":"run","run_id":"r1","request_id":"` + testRequestID + `","ok":true}`, // a run event
		`{"action":"reboot","request_id":"../../etc","ok":true}`,
		`{"action":"reboot","request_id":"` + strings.ToUpper(testRequestID) + `","ok":true}`,
		`{"action":"reboot","ok":true}`,
		`{"action":"reboot","request_id":42}`,
		`not json`,
	} {
		if _, _, ok := parseLateCommandReply([]byte(msg)); ok {
			t.Errorf("%s: accepted", msg)
		}
	}
}

func TestNoAnswerMessage(t *testing.T) {
	if msgNoAnswerYet != "no answer from the agent within 20 s; the outcome is recorded if it answers later" {
		t.Fatalf("message: %q", msgNoAnswerYet)
	}
}

func TestAwaitReplyAnswered(t *testing.T) {
	b := newTestReplyBroker()
	ch, done := b.expect(testRequestID, "api-a")
	msg := []byte(`{"request_id":"` + testRequestID + `","ok":true}`)
	go b.deliver("api-a", msg)
	raw, ok := awaitReply(context.Background(), ch, done, time.Second)
	if !ok || string(raw) != string(msg) {
		t.Fatalf("got %s %v", raw, ok)
	}
}

func TestAwaitReplyTimeoutHandsLateAnswersToTheHook(t *testing.T) {
	b := newTestReplyBroker()
	ch, done := b.expect(testRequestID, "api-a")
	if _, ok := awaitReply(context.Background(), ch, done, time.Millisecond); ok {
		t.Fatal("answered without a reply")
	}
	// No longer awaited: the read loop passes it on to the hook.
	if b.deliver("api-a", []byte(`{"request_id":"`+testRequestID+`","ok":true}`)) {
		t.Fatal("a late reply was delivered to a command that stopped waiting")
	}
}

func TestAwaitReplyKeepsAnAnswerThatRacesTheTimeout(t *testing.T) {
	b := newTestReplyBroker()
	ch, done := b.expect(testRequestID, "api-a")
	msg := []byte(`{"request_id":"` + testRequestID + `","ok":true}`)
	// The reply lands after the timer fired, before the wait is dropped.
	racingDone := func() {
		if !b.deliver("api-a", msg) {
			t.Error("reply refused while still awaited")
		}
		done()
	}
	raw, ok := awaitReply(context.Background(), ch, racingDone, time.Millisecond)
	if !ok || string(raw) != string(msg) {
		t.Fatalf("lost a reply that came in before the wait ended: %s %v", raw, ok)
	}
}

func TestAwaitReplyRequestEnded(t *testing.T) {
	b := newTestReplyBroker()
	ch, done := b.expect(testRequestID, "api-a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := awaitReply(ctx, ch, done, time.Minute); ok {
		t.Fatal("answered without a reply")
	}
	if b.deliver("api-a", []byte(`{"request_id":"`+testRequestID+`","ok":true}`)) {
		t.Fatal("still awaited after the request ended")
	}
}

// fakeJournal records what the restart hook writes.
type fakeJournal struct {
	finished []store.FinishedJob
	outcomes []store.CommandOutcome
	changed  bool
}

func (j *fakeJournal) RecordFinished(_ context.Context, f store.FinishedJob) error {
	j.finished = append(j.finished, f)
	return nil
}

func (j *fakeJournal) RecordOutcomeOf(_ context.Context, o store.CommandOutcome) (bool, error) {
	j.outcomes = append(j.outcomes, o)
	return j.changed, nil
}

type fakeHosts map[string]*models.Host

func (f fakeHosts) GetByApiID(_ context.Context, apiID string) (*models.Host, error) {
	if h, ok := f[apiID]; ok {
		return h, nil
	}
	return nil, errors.New("not found")
}

func TestRestartHookRecordsRunEvents(t *testing.T) {
	jobs := &fakeJournal{}
	hook := restartEventRecorder(fakeHosts{"api-a": {ID: "host-a", ApiID: "api-a"}}, jobs)

	hook(context.Background(), "api-a", []byte(`{"type":"reboot_result","action":"run","run_id":"r1","ok":false,"skipped":true,"error":"another patch run is in progress"}`))
	if len(jobs.finished) != 1 || len(jobs.outcomes) != 0 {
		t.Fatalf("records: %+v %+v", jobs.finished, jobs.outcomes)
	}
	f := jobs.finished[0]
	if f.JobID != "r1" || f.HostID != "host-a" || f.ApiID != "api-a" || f.JobName != restartJobName ||
		f.Failure != "restart skipped: another patch run is in progress" {
		t.Fatalf("skipped run restart: %+v", f)
	}

	// An unknown agent records nothing.
	hook(context.Background(), "api-x", []byte(`{"action":"run","run_id":"r2","ok":true}`))
	if len(jobs.finished) != 1 {
		t.Fatal("recorded a run restart for an unknown host")
	}
}

func TestRestartHookRecordsLateCommandAnswers(t *testing.T) {
	jobs := &fakeJournal{changed: true}
	hook := restartEventRecorder(fakeHosts{}, jobs)

	// The message names another agent: only the connection's own counts.
	hook(context.Background(), "api-a", []byte(`{"type":"reboot_result","request_id":"`+testRequestID+`","action":"reboot","api_id":"api-b","ok":true,"reboot_at":"2026-10-12T23:00:00Z"}`))
	if len(jobs.outcomes) != 1 || len(jobs.finished) != 0 {
		t.Fatalf("records: %+v %+v", jobs.finished, jobs.outcomes)
	}
	o := jobs.outcomes[0]
	if o.JobID != testRequestID || o.ApiID != "api-a" || o.Status != store.JobCompleted || o.Message != "" ||
		o.Output["reboot_at"] != "2026-10-12T23:00:00Z" {
		t.Fatalf("late answer: %+v", o)
	}
	if strings.Join(o.JobNames, ",") != "reboot,reboot_cancel" {
		t.Fatalf("a late answer may complete other jobs: %v", o.JobNames)
	}

	hook(context.Background(), "api-a", []byte(`{"type":"reboot_result","request_id":"`+testRequestID+`","action":"reboot","ok":false,"error":"remote restarts are disabled on this host"}`))
	o = jobs.outcomes[1]
	if o.Status != store.JobFailed || o.Message != "refused by the agent: remote restarts are disabled on this host" || o.Output != nil {
		t.Fatalf("late refusal: %+v", o)
	}

	for _, msg := range []string{
		`{"type":"reboot_result","action":"reboot","ok":true}`,
		`{"type":"reboot_result","request_id":"not-a-uuid","action":"reboot","ok":true}`,
		`garbage`,
	} {
		hook(context.Background(), "api-a", []byte(msg))
	}
	if len(jobs.outcomes) != 2 || len(jobs.finished) != 0 {
		t.Fatalf("recorded an invalid message: %+v", jobs.outcomes)
	}
}
