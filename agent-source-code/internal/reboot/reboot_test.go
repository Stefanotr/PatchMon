package reboot

import (
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	good := []Options{
		{},
		{Mode: ModeNever},
		{Mode: ModeIfNeeded, DelayMinutes: 5},
		{Mode: ModeAlways, At: time.Now().Add(time.Hour), Message: "Maintenance window"},
		{Mode: ModeAlways, DelayMinutes: int(MaxDelay / time.Minute)},
	}
	for _, o := range good {
		if err := o.Validate(); err != nil {
			t.Errorf("%+v rejected: %v", o, err)
		}
	}
	bad := []Options{
		{Mode: "now"},
		{Mode: ModeAlways, DelayMinutes: -1},
		{Mode: ModeAlways, DelayMinutes: int(MaxDelay/time.Minute) + 1},
		{Mode: ModeAlways, Message: strings.Repeat("a", MaxMessageBytes+1)},
		{Mode: ModeAlways, Message: string([]byte{0xff})},
	}
	for _, o := range bad {
		if o.Validate() == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}

func TestWanted(t *testing.T) {
	for _, tt := range []struct {
		mode   Mode
		needed bool
		want   bool
	}{
		{ModeNever, true, false},
		{"", true, false},
		{ModeIfNeeded, false, false},
		{ModeIfNeeded, true, true},
		{ModeAlways, false, true},
	} {
		if got := (Options{Mode: tt.mode}).Wanted(tt.needed); got != tt.want {
			t.Errorf("Wanted(mode %q, needed %v) = %v", tt.mode, tt.needed, got)
		}
	}
}

func TestWhen(t *testing.T) {
	now := time.Date(2026, 10, 9, 16, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name string
		o    Options
		want time.Time
	}{
		{"default is the minimum", Options{}, now.Add(MinDelay)},
		{"minutes", Options{DelayMinutes: 15}, now.Add(15 * time.Minute)},
		{"too short is raised", Options{DelayMinutes: 1}, now.Add(MinDelay)},
		{"absolute time wins", Options{DelayMinutes: 15, At: now.Add(3 * time.Hour)}, now.Add(3 * time.Hour)},
		// "at 03:00" chosen yesterday for a run that only ran now.
		{"past time waits for the next day", Options{At: time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC)}, time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC)},
		{"several days late", Options{At: time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC)}, time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC)},
		{"just passed waits for the next day", Options{At: now.Add(-time.Minute)}, now.Add(24*time.Hour - time.Minute)},
		// The run ended at 15:59, the restart was asked for 16:00.
		{"just ahead is pushed to the minimum", Options{At: now.Add(time.Minute)}, now.Add(MinDelay)},
	} {
		got, err := tt.o.When(now)
		if err != nil || !got.Equal(tt.want) {
			t.Errorf("%s: When = %s, %v; want %s", tt.name, got, err, tt.want)
		}
	}
	if _, err := (Options{At: now.Add(MaxDelay + time.Hour)}).When(now); err == nil {
		t.Error("a time more than MaxDelay away was accepted")
	}
}

func TestShutdownMessage(t *testing.T) {
	if got := (Options{}).ShutdownMessage(); got != messagePrefix+defaultMessage {
		t.Errorf("default message = %q", got)
	}
	got := (Options{Message: "-c\nrm -rf /\t\x1b[2J"}).ShutdownMessage()
	if !strings.HasPrefix(got, messagePrefix+"-c rm -rf /") || strings.ContainsAny(got, "\n\t\x1b") {
		t.Errorf("message not made a single printable line behind the prefix: %q", got)
	}
	long := (Options{Message: strings.Repeat("é", MaxMessageBytes)}).ShutdownMessage()
	if len(long) > MaxMessageBytes+len(messagePrefix) || !strings.HasPrefix(long, messagePrefix) {
		t.Errorf("long message not bounded: %d bytes", len(long))
	}
}
