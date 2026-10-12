package forksource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// stamp sets the link-time variables for one test.
func stamp(t *testing.T, repo, commit string) {
	t.Helper()
	oldRepo, oldCommit, oldAPI := Repository, Commit, githubAPI
	Repository, Commit = repo, commit
	releases = releaseCache{}
	t.Cleanup(func() {
		Repository, Commit, githubAPI = oldRepo, oldCommit, oldAPI
		releases = releaseCache{}
	})
}

func TestRepo(t *testing.T) {
	for _, tt := range []struct {
		stamped string
		want    string
		fork    bool
	}{
		{"", Upstream, false},
		{"PatchMon/PatchMon", Upstream, false},
		{"patchmon/patchmon", "patchmon/patchmon", false},
		{"Stefanotr/PatchMon", "Stefanotr/PatchMon", true},
		{"Stefanotr/../evil", Upstream, false},
		{"Stefanotr/PatchMon?x=1", Upstream, false},
		{"https://github.com/Stefanotr/PatchMon", Upstream, false},
		{"-bad/PatchMon", Upstream, false},
	} {
		stamp(t, tt.stamped, "")
		if got := Repo(); got != tt.want || IsFork() != tt.fork {
			t.Errorf("Repository=%q: Repo()=%q IsFork()=%v, want %q %v", tt.stamped, got, IsFork(), tt.want, tt.fork)
		}
	}
}

func TestDescribe(t *testing.T) {
	stamp(t, "Stefanotr/PatchMon", "0123456789abcdef0123456789abcdef01234567")
	info := Describe()
	if !info.IsFork || info.URL != "https://github.com/Stefanotr/PatchMon" ||
		info.CommitURL != "https://github.com/Stefanotr/PatchMon/commit/0123456789abcdef0123456789abcdef01234567" ||
		info.Upstream != Upstream {
		t.Fatalf("Describe() = %+v", info)
	}
	if got := ReleaseURL("2.1.4-fork.1"); got != "https://github.com/Stefanotr/PatchMon/releases/tag/v2.1.4-fork.1" {
		t.Errorf("ReleaseURL = %s", got)
	}

	stamp(t, "", "not a sha; rm -rf /")
	if info := Describe(); info.Commit != "" || info.CommitURL != "" || info.IsFork {
		t.Fatalf("invalid stamp kept: %+v", info)
	}
}

func fakeGitHub(t *testing.T, status int, body string) *atomic.Int32 {
	t.Helper()
	var b atomic.Pointer[string]
	b.Store(&body)
	return fakeGitHubReleases(t, status, &b)
}

// fakeGitHubReleases serves the body b points to, so a test can publish a
// release between two checks.
func fakeGitHubReleases(t *testing.T, status int, b *atomic.Pointer[string]) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/repos/Stefanotr/PatchMon/releases" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(*b.Load()))
	}))
	t.Cleanup(srv.Close)
	githubAPI = srv.URL
	return &calls
}

func TestLatestVersionFromForkReleases(t *testing.T) {
	stamp(t, "Stefanotr/PatchMon", "")
	calls := fakeGitHub(t, http.StatusOK, `[
		{"tag_name": "v2.1.4-fork.2", "draft": false},
		{"tag_name": "v2.1.4-fork.10", "draft": false},
		{"tag_name": "v2.1.5-fork.1", "draft": true},
		{"tag_name": "nightly", "draft": false},
		{"tag_name": "v2.1.4-fork.3", "draft": false}
	]`)
	got, err := LatestVersion(context.Background(), "server.vcheck.patchmon.net")
	if err != nil || got != "2.1.4-fork.10" {
		t.Fatalf("LatestVersion = %q, %v", got, err)
	}
	// Cached: a second check does not ask GitHub again.
	if _, err := LatestVersion(context.Background(), "agent.vcheck.patchmon.net"); err != nil || calls.Load() != 1 {
		t.Fatalf("calls = %d, err %v", calls.Load(), err)
	}
}

// ageCache makes the last read look older, instead of sleeping.
func ageCache(d time.Duration) {
	releases.mu.Lock()
	defer releases.mu.Unlock()
	releases.at = releases.at.Add(-d)
}

// A release published after the last read shows up on a manual check, not
// only once the cache expires; reads stay at least refreshFloor apart.
func TestCheckLatestVersionSeesANewRelease(t *testing.T) {
	stamp(t, "Stefanotr/PatchMon", "")
	var body atomic.Pointer[string]
	before := `[{"tag_name": "v2.1.4-fork.3", "draft": false}]`
	body.Store(&before)
	calls := fakeGitHubReleases(t, http.StatusOK, &body)
	ctx := context.Background()

	if v, err := LatestVersion(ctx, "server.vcheck.patchmon.net"); err != nil || v != "2.1.4-fork.3" {
		t.Fatalf("first read = %q, %v", v, err)
	}
	after := `[{"tag_name": "v2.1.4-fork.4", "draft": false}, {"tag_name": "v2.1.4-fork.3", "draft": false}]`
	body.Store(&after)
	ageCache(5 * time.Minute)

	// Background checks keep the cached answer.
	if v, _ := LatestVersion(ctx, "agent.vcheck.patchmon.net"); v != "2.1.4-fork.3" || calls.Load() != 1 {
		t.Fatalf("cached read = %q after %d calls", v, calls.Load())
	}
	// A manual check reads the releases again.
	if v, err := CheckLatestVersion(ctx, "server.vcheck.patchmon.net"); err != nil || v != "2.1.4-fork.4" || calls.Load() != 2 {
		t.Fatalf("manual check = %q, %v after %d calls", v, err, calls.Load())
	}
	// Another click right away does not.
	if v, _ := CheckLatestVersion(ctx, "server.vcheck.patchmon.net"); v != "2.1.4-fork.4" || calls.Load() != 2 {
		t.Fatalf("second click = %q after %d calls", v, calls.Load())
	}
	// The fresh answer serves the background checks too.
	if v, _ := LatestVersion(ctx, "agent.vcheck.patchmon.net"); v != "2.1.4-fork.4" || calls.Load() != 2 {
		t.Fatalf("background read = %q after %d calls", v, calls.Load())
	}
	ageCache(refreshFloor)
	if _, err := CheckLatestVersion(ctx, "server.vcheck.patchmon.net"); err != nil || calls.Load() != 3 {
		t.Fatalf("click after the floor: %v after %d calls", err, calls.Load())
	}
}

func TestLatestVersionErrors(t *testing.T) {
	for name, tt := range map[string]struct {
		status int
		body   string
	}{
		"not found":   {http.StatusNotFound, `{"message":"Not Found"}`},
		"rate limit":  {http.StatusForbidden, `{"message":"API rate limit exceeded"}`},
		"no releases": {http.StatusOK, `[]`},
		"only drafts": {http.StatusOK, `[{"tag_name":"v2.1.4-fork.1","draft":true}]`},
		"bad json":    {http.StatusOK, `{`},
	} {
		t.Run(name, func(t *testing.T) {
			stamp(t, "Stefanotr/PatchMon", "")
			fakeGitHub(t, tt.status, tt.body)
			if v, err := LatestVersion(context.Background(), "server.vcheck.patchmon.net"); err == nil {
				t.Fatalf("LatestVersion = %q, want an error", v)
			}
		})
	}
}

// An official build never calls GitHub.
func TestLatestVersionOfficialBuildUsesDNS(t *testing.T) {
	stamp(t, "PatchMon/PatchMon", "")
	calls := fakeGitHub(t, http.StatusOK, `[{"tag_name":"v9.9.9"}]`)
	oldDNS := lookupDNS
	t.Cleanup(func() { lookupDNS = oldDNS })
	asked := ""
	lookupDNS = func(domain string) (string, error) { asked = domain; return "2.1.4", nil }
	v, err := LatestVersion(context.Background(), "server.vcheck.patchmon.net")
	if err != nil || v != "2.1.4" || asked != "server.vcheck.patchmon.net" || calls.Load() != 0 {
		t.Fatalf("v %q err %v asked %q github calls %d", v, err, asked, calls.Load())
	}
	v, err = CheckLatestVersion(context.Background(), "agent.vcheck.patchmon.net")
	if err != nil || v != "2.1.4" || asked != "agent.vcheck.patchmon.net" || calls.Load() != 0 {
		t.Fatalf("manual check: v %q err %v asked %q github calls %d", v, err, asked, calls.Load())
	}
}
