package handler

import (
	"encoding/json"
	"sync"
)

// Replies agents send to commands that wait for one (reboot_result). The
// command handler registers the request ID it sends; the agent websocket
// read loop delivers the reply. A reply is only accepted from the agent the
// command was sent to, and only once.

type pendingReply struct {
	apiID string
	ch    chan []byte
}

type agentReplyBroker struct {
	mu      sync.Mutex
	waiting map[string]pendingReply
}

var agentReplies = &agentReplyBroker{waiting: make(map[string]pendingReply)}

// expect registers a command sent to apiID under requestID. The returned
// function forgets it; call it once the reply is in or no longer awaited.
func (b *agentReplyBroker) expect(requestID, apiID string) (<-chan []byte, func()) {
	ch := make(chan []byte, 1)
	b.mu.Lock()
	b.waiting[requestID] = pendingReply{apiID: apiID, ch: ch}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.waiting, requestID)
		b.mu.Unlock()
	}
}

// deliver hands a reply from apiID to the command waiting for it, and
// reports whether one was. The reply is on the channel before the lock is
// released, so once the command's done function has returned, a reply is
// either on its channel or reported undelivered (and goes to the hook that
// records late answers), never lost in between.
func (b *agentReplyBroker) deliver(apiID string, message []byte) bool {
	var msg struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(message, &msg); err != nil || msg.RequestID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.waiting[msg.RequestID]
	if !ok || p.apiID != apiID {
		return false
	}
	delete(b.waiting, msg.RequestID)
	p.ch <- message // buffered, and removed from the map: never blocks
	return true
}
