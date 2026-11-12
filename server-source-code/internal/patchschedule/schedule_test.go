package patchschedule

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func paris(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	return loc
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func weekly(days []int, at string) Schedule {
	return Schedule{Name: "w", Scope: ScopeSecurity, Frequency: FrequencyWeekly, Weekdays: days, TimeOfDay: at}
}

func monthly(week, weekday int, at string) Schedule {
	return Schedule{Name: "m", Scope: ScopeAll, Frequency: FrequencyMonthly, MonthWeek: week, MonthWeekday: weekday, TimeOfDay: at}
}

func TestNext(t *testing.T) {
	loc := paris(t)
	for _, tt := range []struct {
		name  string
		s     Schedule
		after string
		want  string
	}{
		// 2026-11-10 is a Tuesday; Paris is UTC+1 in winter.
		{"weekly, later today", weekly([]int{2}, "03:00"), "2026-11-09T10:00:00Z", "2026-11-10T02:00:00Z"},
		{"weekly, strictly after the slot", weekly([]int{2}, "03:00"), "2026-11-10T02:00:00Z", "2026-11-17T02:00:00Z"},
		{"weekly, next of several days", weekly([]int{1, 4}, "22:30"), "2026-11-10T12:00:00Z", "2026-11-12T21:30:00Z"},
		{"weekly, summer time", weekly([]int{3}, "03:00"), "2027-07-01T00:00:00Z", "2027-07-07T01:00:00Z"},
		// Wednesdays of November 2026: 4, 11, 18, 25.
		{"monthly, 2nd Wednesday", monthly(2, 3, "22:00"), "2026-11-01T00:00:00Z", "2026-11-11T21:00:00Z"},
		{"monthly, past this month's slot", monthly(2, 3, "22:00"), "2026-11-12T00:00:00Z", "2026-12-09T21:00:00Z"},
		// Thursdays of December 2026: 3, 10, 17, 24, 31.
		{"monthly, 4th Thursday", monthly(4, 4, "06:00"), "2026-12-01T00:00:00Z", "2026-12-24T05:00:00Z"},
		{"monthly, last Thursday", monthly(LastWeek, 4, "06:00"), "2026-12-01T00:00:00Z", "2026-12-31T05:00:00Z"},
		// Fridays of November 2026: 6, 13, 20, 27.
		{"monthly, last Friday of a 4-Friday month", monthly(LastWeek, 5, "20:00"), "2026-11-01T00:00:00Z", "2026-11-27T19:00:00Z"},
	} {
		got, err := tt.s.Next(mustTime(t, tt.after), loc)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if want := mustTime(t, tt.want); !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("%s: got %s, want %s", tt.name, got, want)
		}
	}
}

// A slot inside the spring-forward gap still runs that night.
func TestNextInADaylightSavingGap(t *testing.T) {
	loc := paris(t)
	// 2027-03-28 is the last Sunday of March: 02:00 to 03:00 does not exist.
	s := weekly([]int{0}, "02:30")
	got, err := s.Next(mustTime(t, "2027-03-27T12:00:00Z"), loc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Before(mustTime(t, "2027-03-28T00:00:00Z")) || got.After(mustTime(t, "2027-03-28T02:00:00Z")) {
		t.Fatalf("got %s, want the night of 2027-03-28", got)
	}
}

func TestNextWithoutLocationIsUTC(t *testing.T) {
	s := weekly([]int{2}, "03:00")
	got, err := s.Next(mustTime(t, "2026-11-09T10:00:00Z"), nil)
	if err != nil || !got.Equal(mustTime(t, "2026-11-10T03:00:00Z")) {
		t.Fatalf("got %s, %v", got, err)
	}
}

func TestValidate(t *testing.T) {
	for _, tt := range []struct {
		name string
		s    Schedule
	}{
		{"no name", Schedule{Scope: ScopeAll, Frequency: FrequencyWeekly, Weekdays: []int{1}, TimeOfDay: "03:00"}},
		{"bad scope", Schedule{Name: "x", Scope: "everything", Frequency: FrequencyWeekly, Weekdays: []int{1}, TimeOfDay: "03:00"}},
		{"bad time", weekly([]int{1}, "24:00")},
		{"time without leading zero", weekly([]int{1}, "3:00")},
		{"no day", weekly(nil, "03:00")},
		{"day out of range", weekly([]int{7}, "03:00")},
		{"week 0", monthly(0, 1, "03:00")},
		{"week 6", monthly(6, 1, "03:00")},
		{"weekday out of range", monthly(1, 7, "03:00")},
		{"unknown frequency", Schedule{Name: "x", Scope: ScopeAll, Frequency: "daily", TimeOfDay: "03:00"}},
	} {
		err := tt.s.Validate()
		var fe FormError
		if !errors.As(err, &fe) {
			t.Errorf("%s: err = %v, want a FormError", tt.name, err)
		}
		if _, err := tt.s.Next(time.Now(), time.UTC); err == nil {
			t.Errorf("%s: Next accepted an invalid schedule", tt.name)
		}
	}
	for _, s := range []Schedule{weekly([]int{0, 6}, "00:00"), monthly(LastWeek, 0, "23:59"), monthly(1, 6, "12:30")} {
		if err := s.Validate(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
}

func TestNormalize(t *testing.T) {
	s := weekly([]int{3, 1, 3, 9}, "03:00")
	s.Name = "  nightly  "
	s.MonthWeek, s.MonthWeekday = 2, 4
	s.Normalize()
	if s.Name != "nightly" || !reflect.DeepEqual(s.Weekdays, []int{1, 3, 9}) || s.MonthWeek != 0 || s.MonthWeekday != 0 {
		t.Fatalf("weekly: %+v", s)
	}
	if err := s.Validate(); err == nil {
		t.Fatal("an out-of-range day was dropped instead of reported")
	}
	m := monthly(2, 3, "22:00")
	m.Weekdays = []int{1}
	m.Normalize()
	if len(m.Weekdays) != 0 || m.Weekdays == nil {
		t.Fatalf("monthly: %+v", m)
	}
}
