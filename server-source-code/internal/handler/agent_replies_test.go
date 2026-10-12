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
