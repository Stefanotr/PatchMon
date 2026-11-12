package patchschedule

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

type fakeStorage struct {
	due      []Schedule
	claimed  map[string]bool // id -> already claimed by another instance
	claims   []time.Time     // next slots claimed
	results  map[string]Result
	targets  []Target
	pending  map[string][]string // host -> packages
	security map[string]bool     // host -> asked for security only
}

func (f *fakeStorage) Due(context.Context, time.Time) ([]Schedule, error) { return f.due, nil }

func (f *fakeStorage) Claim(_ context.Context, id string, _, next, _ time.Time) (bool, error) {
	if f.claimed[id] {
		return false, nil
	}
	f.claims = append(f.claims, next)
	return true, nil
}

func (f *fakeStorage) SetResult(_ context.Context, id string, r Result) error {
	if f.results == nil {
		f.results = map[string]Result{}
	}
	f.results[id] = r
	return nil
}

func (f *fakeStorage) Targets(context.Context, *string) ([]Target, error) { return f.targets, nil }

func (f *fakeStorage) PendingPackages(_ context.Context, hostID string, securityOnly bool, limit int) ([]string, error) {
	if f.security == nil {
		f.security = map[string]bool{}
	}
	f.security[hostID] = securityOnly
	if limit != MaxPackages {
		return nil, errors.New("unexpected limit")
	}
	return f.pending[hostID], nil
}

type started struct {
	host     string
	packages []string
	by       *string
}

func pacman() *string { s := "pacman"; return &s }

func TestFire(t *testing.T) {
	st := &fakeStorage{
		targets: []Target{
			{HostID: "deb", OSType: "Debian"},
			{HostID: "busy", OSType: "Ubuntu"},
			{HostID: "clean", OSType: "Debian"},
			{HostID: "win", OSType: "Windows Server 2022"},
			{HostID: "arch", OSType: "Arch Linux", PackageManager: pacman()},
			{HostID: "broken", OSType: "Debian"},
		},
		pending: map[string][]string{"deb": {"openssl", "bind9"}, "busy": {"curl"}, "broken": {"vim"}},
	}
	var runs []started
	f := &Firer{Store: st, Start: func(_ context.Context, tg Target, pkgs []string, snap []byte, by *string) error {
		switch tg.HostID {
		case "busy":
			return store.ErrHostHasActivePatchRun
		case "broken":
			return errors.New("queue down")
		}
		if len(snap) == 0 {
			t.Error("no snapshot")
		}
		runs = append(runs, started{tg.HostID, pkgs, by})
		return nil
	}}
	s := weekly([]int{2}, "03:00")
	now := time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)
	res, err := f.Fire(context.Background(), &s, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	want := Result{FiredAt: now, Queued: 1, UpToDate: 1, Busy: 1, Unsupported: 2, Failed: 1}
	if res != want {
		t.Fatalf("got %+v, want %+v", res, want)
	}
	if !reflect.DeepEqual(runs, []started{{"deb", []string{"openssl", "bind9"}, nil}}) {
		t.Fatalf("runs %+v", runs)
	}
	if !st.security["deb"] {
		t.Fatal("a security schedule asked for every update")
	}
	if _, asked := st.security["win"]; asked {
		t.Fatal("packages were read for a host that cannot dry-run them")
	}

	user := "u1"
	s.Scope = ScopeAll
	res, _ = f.Fire(context.Background(), &s, &user, now)
	if !res.Manual || st.security["deb"] || runs[len(runs)-1].by != &user {
		t.Fatalf("manual run: %+v, security %v", res, st.security["deb"])
	}
}

func TestRunDue(t *testing.T) {
	now := time.Date(2026, 11, 10, 2, 0, 30, 0, time.UTC)
	onTime := now.Add(-30 * time.Second)
	late := now.Add(-2 * time.Hour)
	mk := func(id string, at time.Time) Schedule {
		s := weekly([]int{2}, "03:00")
		s.ID, s.NextRunAt = id, &at
		return s
	}
	st := &fakeStorage{
		due:     []Schedule{mk("on-time", onTime), mk("late", late), mk("taken", onTime)},
		claimed: map[string]bool{"taken": true},
		targets: []Target{{HostID: "deb", OSType: "Debian"}},
		pending: map[string][]string{"deb": {"openssl"}},
	}
	queued := 0
	f := &Firer{Store: st, Start: func(context.Context, Target, []string, []byte, *string) error {
		queued++
		return nil
	}}
	f.RunDue(context.Background(), time.UTC, now)

	if queued != 1 {
		t.Fatalf("%d dry runs queued, want 1: the late slot is missed, the taken one fired elsewhere", queued)
	}
	if r := st.results["on-time"]; r.Queued != 1 || r.Missed {
		t.Fatalf("on time: %+v", r)
	}
	if r := st.results["late"]; !r.Missed || r.Queued != 0 {
		t.Fatalf("late: %+v", r)
	}
	if _, ok := st.results["taken"]; ok {
		t.Fatal("a schedule another instance claimed was fired again")
	}
	// Tuesday 03:00 UTC is still ahead of now (02:00:30), so that is the next slot.
	for _, next := range st.claims {
		if !next.Equal(time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC)) {
			t.Fatalf("next slot %s", next)
		}
	}
}
