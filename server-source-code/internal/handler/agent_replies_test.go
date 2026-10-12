package handler

import (
	"strings"
	"testing"
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
