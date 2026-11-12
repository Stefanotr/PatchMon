// Package patchschedule runs recurring patch schedules: at each slot of a
// schedule, a dry run of the pending updates of every host in its group.
// Nothing is installed by a schedule. Each dry run ends in the "validated"
// state, the same one the patch wizard leaves, and a person approves it
// before anything changes on the host; the host's patch policy then decides
// when the approved run executes.
package patchschedule

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Scopes: which pending updates a schedule dry-runs.
const (
	ScopeSecurity = "security"
	ScopeAll      = "all"
)

// Frequencies.
const (
	FrequencyWeekly  = "weekly"
	FrequencyMonthly = "monthly"
)

// LastWeek in MonthWeek means the last such weekday of the month.
const LastWeek = 5

// MaxPackages is the most packages one scheduled dry run carries, the limit
// the patch trigger applies to a run.
const MaxPackages = 100

// Schedule is a recurring dry run of the pending updates of a host group.
type Schedule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// HostGroupID is the group whose hosts are patched; nil means every host.
	HostGroupID   *string `json:"host_group_id"`
	HostGroupName *string `json:"host_group_name"`
	Scope         string  `json:"scope"`
	Frequency     string  `json:"frequency"`
	// Weekdays (weekly): 0 is Sunday, 6 Saturday.
	Weekdays []int `json:"weekdays"`
	// MonthWeek and MonthWeekday (monthly): the MonthWeek-th MonthWeekday of
	// the month, MonthWeek 1 to 4, or LastWeek for the last one.
	MonthWeek    int `json:"month_week"`
	MonthWeekday int `json:"month_weekday"`
	// TimeOfDay is "HH:MM" in the organisation's timezone.
	TimeOfDay  string     `json:"time_of_day"`
	Enabled    bool       `json:"enabled"`
	NextRunAt  *time.Time `json:"next_run_at"`
	LastRunAt  *time.Time `json:"last_run_at"`
	LastResult *Result    `json:"last_result"`
	CreatedBy  *string    `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Result is what one firing of a schedule did, host by host.
type Result struct {
	FiredAt time.Time `json:"fired_at"`
	// Queued dry runs, one per host with pending updates in scope.
	Queued int `json:"queued"`
	// UpToDate hosts had nothing to patch in scope.
	UpToDate int `json:"up_to_date"`
	// Busy hosts already had a patch run queued, validating or running.
	Busy int `json:"busy"`
	// Unsupported hosts cannot dry-run a package list: Windows, pacman.
	Unsupported int `json:"unsupported"`
	// Failed hosts: the run could not be recorded or queued.
	Failed int `json:"failed"`
	// Missed is set when the server was down at the slot and skipped it.
	Missed bool `json:"missed,omitempty"`
	// Manual is set for a firing started with "Run now".
	Manual bool `json:"manual,omitempty"`
}

// FormError is a mistake in a schedule, worded for the person who filled the
// form; the API returns it as is with a 400.
type FormError string

func (e FormError) Error() string { return string(e) }

var timeOfDayRE = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Normalize trims the name and sorts and dedupes the weekdays, and clears the
// fields the frequency does not use, so what is stored reads the same as what
// runs.
func (s *Schedule) Normalize() {
	s.Name = strings.TrimSpace(s.Name)
	switch s.Frequency {
	case FrequencyWeekly:
		var seen [7]bool
		// Out-of-range days are kept, last, so that Validate reports them.
		var bad []int
		for _, w := range s.Weekdays {
			if w < 0 || w > 6 {
				bad = append(bad, w)
				continue
			}
			seen[w] = true
		}
		days := make([]int, 0, 7)
		for d, on := range seen {
			if on {
				days = append(days, d)
			}
		}
		s.Weekdays = append(days, bad...)
		s.MonthWeek, s.MonthWeekday = 0, 0
	case FrequencyMonthly:
		s.Weekdays = []int{}
	}
	if s.Weekdays == nil {
		s.Weekdays = []int{}
	}
}

// Validate reports the first thing wrong with s, in words for the person who
// filled the form.
func (s *Schedule) Validate() error {
	if s.Name == "" {
		return FormError("Name is required")
	}
	if len([]rune(s.Name)) > 100 {
		return FormError("Name must be 100 characters or fewer")
	}
	if s.Scope != ScopeSecurity && s.Scope != ScopeAll {
		return FormError("Scope must be security or all")
	}
	if !timeOfDayRE.MatchString(s.TimeOfDay) {
		return FormError("Time must be HH:MM, from 00:00 to 23:59")
	}
	switch s.Frequency {
	case FrequencyWeekly:
		if len(s.Weekdays) == 0 {
			return FormError("Pick at least one day of the week")
		}
		for _, d := range s.Weekdays {
			if d < 0 || d > 6 {
				return FormError("Days of the week go from 0 (Sunday) to 6 (Saturday)")
			}
		}
	case FrequencyMonthly:
		if s.MonthWeek < 1 || s.MonthWeek > LastWeek {
			return FormError("The week of the month must be 1 to 4, or 5 for the last one")
		}
		if s.MonthWeekday < 0 || s.MonthWeekday > 6 {
			return FormError("The day of the week must be 0 (Sunday) to 6 (Saturday)")
		}
	default:
		return FormError("Frequency must be weekly or monthly")
	}
	return nil
}

// clock returns the hour and minute of TimeOfDay, already validated.
func (s *Schedule) clock() (int, int, error) {
	if !timeOfDayRE.MatchString(s.TimeOfDay) {
		return 0, 0, fmt.Errorf("invalid time of day %q", s.TimeOfDay)
	}
	h, _ := strconv.Atoi(s.TimeOfDay[:2])
	m, _ := strconv.Atoi(s.TimeOfDay[3:])
	return h, m, nil
}

// matches reports whether the date of day is one of the schedule's days.
func (s *Schedule) matches(day time.Time) bool {
	wd := int(day.Weekday())
	switch s.Frequency {
	case FrequencyWeekly:
		for _, d := range s.Weekdays {
			if d == wd {
				return true
			}
		}
		return false
	case FrequencyMonthly:
		if wd != s.MonthWeekday {
			return false
		}
		if s.MonthWeek == LastWeek {
			return day.AddDate(0, 0, 7).Month() != day.Month()
		}
		return (day.Day()-1)/7+1 == s.MonthWeek
	}
	return false
}

// Next returns the first slot of s strictly after after, in UTC. Slots are
// wall-clock times in loc; a slot inside a daylight saving gap (02:30 on the
// night clocks go forward) runs within the hour around it, as time.Date
// resolves it.
func (s *Schedule) Next(after time.Time, loc *time.Location) (time.Time, error) {
	if err := s.Validate(); err != nil {
		return time.Time{}, err
	}
	if loc == nil {
		loc = time.UTC
	}
	h, m, err := s.clock()
	if err != nil {
		return time.Time{}, err
	}
	local := after.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	// Every monthly pattern comes back within 5 weeks; a year is plenty.
	for i := 0; i < 400; i++ {
		d := day.AddDate(0, 0, i)
		if !s.matches(d) {
			continue
		}
		slot := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, loc)
		if slot.After(after) {
			return slot.UTC(), nil
		}
	}
	return time.Time{}, errors.New("no slot within a year")
}
