package reboot

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Windows does not say which restart is pending. What it does keep is the
// System event log: USER32 writes event 1074 when a process initiates a
// shutdown or restart, with the message given as its comment, and event 1075
// when one is aborted. The query runs on Windows (schedule_windows.go); the
// decision below is kept apart from it so it is tested everywhere.

const (
	eventShutdownInitiated = 1074
	eventShutdownAborted   = 1075

	eventResultMarker = "PATCHMON_RESULT:"
	eventErrorMarker  = "PATCHMON_ERROR:"
)

// shutdownEvent is a USER32 event of the System log, as the query reports
// it: its id, its record number (which orders events) and its data values.
type shutdownEvent struct {
	ID     int         `json:"id"`
	Record int64       `json:"record"`
	Data   []eventData `json:"data"`
}

type eventData struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// comment returns the comment of a 1074 event: its param6 value, or its
// sixth value when the values carry no names.
func (e shutdownEvent) comment() string {
	for _, d := range e.Data {
		if d.Name == "param6" {
			return d.Value
		}
	}
	if len(e.Data) >= 6 && e.Data[5].Name == "" {
		return e.Data[5].Value
	}
	return ""
}

// parseShutdownEvents reads the query's output: the events on its result
// line, or the failure on its error line. Any other line is ignored.
func parseShutdownEvents(out []byte) ([]shutdownEvent, error) {
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if msg, ok := strings.CutPrefix(line, eventErrorMarker); ok {
			if msg = strings.TrimSpace(msg); msg == "" {
				msg = "unknown error"
			}
			return nil, errors.New(msg)
		}
		if raw, ok := strings.CutPrefix(line, eventResultMarker); ok {
			var events []shutdownEvent
			if err := json.Unmarshal([]byte(raw), &events); err != nil {
				return nil, fmt.Errorf("unreadable event list: %w", err)
			}
			return events, nil
		}
	}
	return nil, errors.New("the event log query returned no result")
}

// eventLogOwner judges the USER32 events written since PatchMon scheduled
// its restart. The latest 1074 must carry the PatchMon message, with no 1075
// after it. If it carries the message but was aborted, PatchMon's restart is
// gone; if it does not, the latest restart asked for is someone else's.
// Without any 1074, nothing is confirmed.
func eventLogOwner(events []shutdownEvent) (Owner, error) {
	events = slices.Clone(events)
	slices.SortStableFunc(events, func(a, b shutdownEvent) int {
		switch {
		case a.Record < b.Record:
			return -1
		case a.Record > b.Record:
			return 1
		}
		return 0
	})
	last := -1
	for i, e := range events {
		if e.ID == eventShutdownInitiated {
			last = i
		}
	}
	if last < 0 {
		return OwnerUnknown, errors.New("the System event log shows no restart initiated since PatchMon scheduled its own")
	}
	aborted := slices.ContainsFunc(events[last+1:], func(e shutdownEvent) bool { return e.ID == eventShutdownAborted })
	switch ours := strings.HasPrefix(events[last].comment(), messagePrefix); {
	case ours && aborted:
		return OwnerNone, nil
	case ours:
		return OwnerPatchMon, nil
	}
	return OwnerOther, nil
}
