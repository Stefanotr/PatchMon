package reboot

import (
	"strings"
	"testing"
)

// initiated is a 1074 event with its seven named values, the comment last
// but one, the way USER32 writes it.
func initiated(record int64, comment string) shutdownEvent {
	names := []string{"param1", "param2", "param3", "param4", "param5", "param6", "param7"}
	values := []string{`C:\Windows\system32\shutdown.exe (HOST)`, "HOST", "Other (Planned)", "0x80000000", "restart", comment, `HOST\admin`}
	e := shutdownEvent{ID: eventShutdownInitiated, Record: record}
	for i := range names {
		e.Data = append(e.Data, eventData{Name: names[i], Value: values[i]})
	}
	return e
}

func aborted(record int64) shutdownEvent {
	return shutdownEvent{ID: eventShutdownAborted, Record: record}
}

const patchMonComment = "PatchMon: this computer restarts to finish installing updates"

func TestEventLogOwner(t *testing.T) {
	for _, tt := range []struct {
		name   string
		events []shutdownEvent
		want   Owner
	}{
		{"PatchMon's, pending", []shutdownEvent{initiated(10, patchMonComment)}, OwnerPatchMon},
		{"PatchMon's, aborted", []shutdownEvent{initiated(10, patchMonComment), aborted(11)}, OwnerNone},
		// shutdown /a, then shutdown /r /t 7200 by an administrator.
		{"replaced by an administrator's", []shutdownEvent{initiated(10, patchMonComment), aborted(11), initiated(12, "")}, OwnerOther},
		{"an installer's", []shutdownEvent{initiated(10, patchMonComment), aborted(11), initiated(12, "Setup needs to restart")}, OwnerOther},
		{"an administrator's, aborted", []shutdownEvent{initiated(10, patchMonComment), aborted(11), initiated(12, "maintenance"), aborted(13)}, OwnerOther},
		{"PatchMon's again after an abort", []shutdownEvent{initiated(10, patchMonComment), aborted(11), initiated(12, patchMonComment)}, OwnerPatchMon},
		// The order is the record number, not the order given.
		{"out of order", []shutdownEvent{initiated(12, "maintenance"), aborted(11), initiated(10, patchMonComment)}, OwnerOther},
		{"the prefix elsewhere than at the start", []shutdownEvent{initiated(10, "see PatchMon: later")}, OwnerOther},
		// Unnamed values: the comment is the sixth.
		{"unnamed values", []shutdownEvent{{ID: eventShutdownInitiated, Record: 10, Data: []eventData{{Value: "p"}, {Value: "h"}, {Value: "r"}, {Value: "c"}, {Value: "t"}, {Value: patchMonComment}, {Value: "u"}}}}, OwnerPatchMon},
	} {
		got, err := eventLogOwner(tt.events)
		if got != tt.want || err != nil {
			t.Errorf("%s: owner %v, %v; want %v", tt.name, got, err, tt.want)
		}
	}
	for _, events := range [][]shutdownEvent{nil, {aborted(11)}} {
		if got, err := eventLogOwner(events); got != OwnerUnknown || err == nil {
			t.Errorf("%v: owner %v, %v; want unknown with a reason", events, got, err)
		}
	}
}

func TestParseShutdownEvents(t *testing.T) {
	out := "WARNING: something\r\n" + eventResultMarker +
		`[{"id":1074,"record":10,"data":[{"name":"param6","value":"PatchMon: x \u00e9"}]},{"id":1075,"record":11,"data":[]}]` + "\r\n"
	events, err := parseShutdownEvents([]byte(out))
	if err != nil || len(events) != 2 || events[0].comment() != "PatchMon: x é" || events[1].ID != eventShutdownAborted {
		t.Fatalf("events %+v, %v", events, err)
	}
	if events, err := parseShutdownEvents([]byte(eventResultMarker + "[]\n")); err != nil || len(events) != 0 {
		t.Fatalf("no event: %+v, %v", events, err)
	}
	for _, bad := range []string{
		eventErrorMarker + "Attempted to perform an unauthorized operation.\n",
		eventResultMarker + "{not json\n",
		"nothing\n",
		"",
	} {
		if _, err := parseShutdownEvents([]byte(bad)); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	if _, err := parseShutdownEvents([]byte(eventErrorMarker + "access denied")); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Errorf("the failure is not passed on: %v", err)
	}
}
