package reboot

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Paris, whatever the machine has installed
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

// withLocalZone makes loc the host's time zone for the test.
func withLocalZone(t *testing.T, loc *time.Location) {
	t.Helper()
	old := localZone
	localZone = loc
	t.Cleanup(func() { localZone = old })
}

func TestWhen(t *testing.T) {
	withLocalZone(t, time.UTC)
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
		// A time picked a few seconds late, or delayed on its way.
		{"just passed restarts at the earliest", Options{At: now.Add(-time.Minute)}, now.Add(MinDelay)},
		{"passed within the tolerance restarts at the earliest", Options{At: now.Add(-LateTolerance)}, now.Add(MinDelay)},
		{"passed beyond the tolerance waits for the next day", Options{At: now.Add(-LateTolerance - time.Minute)}, now.Add(24*time.Hour - LateTolerance - time.Minute)},
		// The next occurrence is less than MinDelay ahead: pushed back, not
		// moved to the day after.
		{"next occurrence just ahead is pushed to the minimum", Options{At: now.Add(-24*time.Hour + 2*time.Minute)}, now.Add(MinDelay)},
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

// Days are added in the host's time zone: 03:00 stays 03:00 when daylight
// saving time ends (Europe/Paris, 2026-10-25 03:00 CEST becomes 02:00 CET)
// or starts (2026-03-29 02:00 CET becomes 03:00 CEST).
func TestWhenAcrossDaylightSavingTime(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	withLocalZone(t, paris)
	for _, tt := range []struct {
		name    string
		at, now time.Time
		want    time.Time
	}{
		{
			name: "autumn: the next 03:00 is an hour later in UTC",
			at:   time.Date(2026, 10, 24, 3, 0, 0, 0, paris), // 01:00 UTC
			now:  time.Date(2026, 10, 24, 12, 0, 0, 0, paris),
			want: time.Date(2026, 10, 25, 3, 0, 0, 0, paris), // 02:00 UTC
		},
		{
			name: "autumn, several days late",
			at:   time.Date(2026, 10, 22, 3, 0, 0, 0, paris),
			now:  time.Date(2026, 10, 25, 1, 0, 0, 0, paris),
			want: time.Date(2026, 10, 25, 3, 0, 0, 0, paris),
		},
		{
			name: "spring: the next 03:00 is an hour earlier in UTC",
			at:   time.Date(2026, 3, 28, 3, 0, 0, 0, paris), // 02:00 UTC
			now:  time.Date(2026, 3, 28, 12, 0, 0, 0, paris),
			want: time.Date(2026, 3, 29, 3, 0, 0, 0, paris), // 01:00 UTC
		},
		{
			// The time was sent in UTC; the wall-clock time is the host's.
			name: "a time sent in UTC keeps its local wall-clock time",
			at:   time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC), // 03:00 CEST
			now:  time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC),
			want: time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC), // 03:00 CET
		},
	} {
		got, err := (Options{At: tt.at}).When(tt.now)
		if err != nil || !got.Equal(tt.want) {
			t.Errorf("%s: When = %s, %v; want %s", tt.name, got.In(paris), err, tt.want.In(paris))
		}
		if h := got.In(paris).Hour(); h != 3 {
			t.Errorf("%s: restart at %02d:00 local time, want 03:00", tt.name, h)
		}
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
