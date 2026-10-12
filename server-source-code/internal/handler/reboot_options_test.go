package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func intPtr(n int) *int { return &n }

func TestRebootRequestForRun(t *testing.T) {
	now := time.Date(2026, 10, 9, 14, 0, 0, 0, time.UTC)

	for _, r := range []*rebootRequest{nil, {}, {Mode: "never", DelayMinutes: intPtr(1)}} {
		if opts, err := r.forRun(now); err != nil || opts != nil {
			t.Errorf("%+v: opts %+v, err %v; want no restart", r, opts, err)
		}
	}

	opts, err := (&rebootRequest{Mode: "if_needed"}).forRun(now)
	if err != nil || opts.Mode != "if_needed" || opts.DelayMinutes != 5 || opts.At != nil {
		t.Fatalf("defaults: %+v, %v", opts, err)
	}

	opts, err = (&rebootRequest{Mode: "always", DelayMinutes: intPtr(30), At: "2026-10-10T03:00:00+02:00", Message: "  window  "}).forRun(now)
	if err != nil || opts.At == nil || !opts.At.Equal(time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)) || opts.DelayMinutes != 0 || opts.Message != "window" {
		t.Fatalf("absolute time: %+v, %v", opts, err)
	}

	for name, r := range map[string]*rebootRequest{
		"unknown mode":   {Mode: "now"},
		"delay too low":  {Mode: "always", DelayMinutes: intPtr(rebootMinDelayMinutes - 1)},
		"delay too high": {Mode: "always", DelayMinutes: intPtr(rebootMaxDelayMinutes + 1)},
		"bad time":       {Mode: "always", At: "03:00"},
		"time past":      {Mode: "always", At: "2026-10-09T13:00:00Z"},
		"time too far":   {Mode: "always", At: "2026-10-16T14:01:00Z"}, // the agent refuses beyond 7 days
		"multi-line":     {Mode: "always", Message: "a\nb"},
		"escape":         {Mode: "always", Message: "\x1b[2J"},
		"too long":       {Mode: "always", Message: strings.Repeat("a", rebootMaxMessageBytes+1)},
	} {
		if _, err := r.forRun(now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRebootRequestForCommandAlwaysRestarts(t *testing.T) {
	for _, r := range []*rebootRequest{nil, {Mode: "never"}} {
		opts, err := r.forCommand(time.Now())
		if err != nil || opts.Mode != "always" {
			t.Errorf("%+v: %+v, %v", r, opts, err)
		}
	}
}

func TestSnapshotWithReboot(t *testing.T) {
	snap := []byte(`{"name":"Weekly","patch_delay_type":"fixed_time"}`)
	if got := snapshotWithReboot(snap, nil); string(got) != string(snap) {
		t.Errorf("no options changed the snapshot: %s", got)
	}
	opts, _ := (&rebootRequest{Mode: "if_needed", DelayMinutes: intPtr(15)}).forRun(time.Now())
	var m map[string]any
	if err := json.Unmarshal(snapshotWithReboot(snap, opts), &m); err != nil {
		t.Fatal(err)
	}
	reboot, _ := m["reboot"].(map[string]any)
	if m["name"] != "Weekly" || reboot["mode"] != "if_needed" || reboot["delay_minutes"] != float64(15) {
		t.Errorf("snapshot = %v", m)
	}
}
