// Package forksource records which repository a server build comes from.
//
// An official build leaves Repository unset (or set to PatchMon/PatchMon) and
// keeps the upstream behaviour: update checks read the TXT records under
// vcheck.patchmon.net and the UI links to the upstream repository. A fork build
// stamps its own repository at link time. Its update checks then follow the
// fork's GitHub releases, and the UI links to the fork, so an instance running
// a fork is told about the fork's releases, not about upstream releases that
// do not carry the fork's changes.
package forksource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/util"
)

// Upstream is the official repository.
const Upstream = "PatchMon/PatchMon"

// Set at link time (see .github/workflows/docker.yml):
//
//	-X github.com/PatchMon/PatchMon/server-source-code/internal/forksource.Repository=owner/name
//	-X github.com/PatchMon/PatchMon/server-source-code/internal/forksource.Commit=<sha>
var (
	Repository = ""
	Commit     = ""
)

var (
	// GitHub owner and repository names.
	repoRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)
	commitRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	// A release tag carrying a version the server can compare.
	tagRe = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)$`)
)

// Repo returns the repository this build comes from: the stamped one when it
// is a valid owner/name, upstream otherwise.
func Repo() string {
	if repoRe.MatchString(Repository) && !strings.Contains(Repository, "..") {
		return Repository
	}
	return Upstream
}

// IsFork reports whether this build comes from a repository other than
// upstream.
func IsFork() bool {
	return !strings.EqualFold(Repo(), Upstream)
}

// RepoURL is the repository's GitHub page.
func RepoURL() string {
	return "https://github.com/" + Repo()
}

// ReleaseURL is the GitHub page of the release of version.
func ReleaseURL(version string) string {
	return RepoURL() + "/releases/tag/v" + url.PathEscape(strings.TrimPrefix(version, "v"))
}

// Info describes where the build comes from, for the API.
type Info struct {
	Repository string `json:"source_repository"`
	URL        string `json:"source_url"`
	Commit     string `json:"source_commit,omitempty"`
	CommitURL  string `json:"source_commit_url,omitempty"`
	IsFork     bool   `json:"is_fork"`
	Upstream   string `json:"upstream_repository"`
}

// Describe returns the build's origin.
func Describe() Info {
	info := Info{Repository: Repo(), URL: RepoURL(), IsFork: IsFork(), Upstream: Upstream}
	if commitRe.MatchString(Commit) {
		info.Commit = Commit
		info.CommitURL = RepoURL() + "/commit/" + Commit
	}
	return info
}

// LatestVersion returns the latest released version. An official build reads
// the TXT record at dnsDomain (server.vcheck.patchmon.net or
// agent.vcheck.patchmon.net). A fork reads the highest version among its
// published GitHub releases: its server and its agents ship in the same
// release, so both checks get the same answer. A fork's answer may be up to
// releasesTTL old, which suits background checks and page loads.
func LatestVersion(ctx context.Context, dnsDomain string) (string, error) {
	return latestVersion(ctx, dnsDomain, releasesTTL)
}

// CheckLatestVersion answers a check an admin asked for ("Check for
// updates"). A fork reads its releases again unless they were read less than
// refreshFloor ago, so a release published minutes earlier shows up at once
// while repeated clicks stay within GitHub's limit. The fresh answer also
// serves the next LatestVersion calls.
func CheckLatestVersion(ctx context.Context, dnsDomain string) (string, error) {
	return latestVersion(ctx, dnsDomain, refreshFloor)
}

func latestVersion(ctx context.Context, dnsDomain string, maxAge time.Duration) (string, error) {
	if !IsFork() {
		return lookupDNS(dnsDomain)
	}
	return releases.latest(ctx, Repo(), maxAge)
}

// lookupDNS is a variable so tests do not touch the network.
var lookupDNS = util.LookupVersionFromDNS

const (
	// GitHub allows 60 unauthenticated requests an hour per address. Every
	// read goes through one cache and two reads are at least refreshFloor
	// apart, so the server stays within that limit whatever calls the check.
	releasesTTL      = 15 * time.Minute
	refreshFloor     = time.Minute
	releasesErrorTTL = time.Minute
	releasesTimeout  = 10 * time.Second
	maxReleasesBody  = 1 << 20
)

// githubAPI is a variable so tests can point it at a fake.
var githubAPI = "https://api.github.com"

type releaseCache struct {
	mu      sync.Mutex
	repo    string
	version string
	err     error
	at      time.Time
}

var releases releaseCache

// latest returns the cached answer when it is younger than maxAge (at most
// releasesErrorTTL for an error), and reads the releases again otherwise.
func (c *releaseCache) latest(ctx context.Context, repo string, maxAge time.Duration) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil && maxAge > releasesErrorTTL {
		maxAge = releasesErrorTTL
	}
	if c.repo == repo && !c.at.IsZero() && time.Since(c.at) < maxAge {
		return c.version, c.err
	}
	c.version, c.err = fetchLatestRelease(ctx, repo)
	c.repo, c.at = repo, time.Now()
	return c.version, c.err
}

func fetchLatestRelease(ctx context.Context, repo string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, releasesTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPI+"/repos/"+repo+"/releases?per_page=30", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "PatchMon-update-check")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github releases of %s: %w", repo, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases of %s: HTTP %d", repo, resp.StatusCode)
	}
	var list []struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleasesBody)).Decode(&list); err != nil {
		return "", fmt.Errorf("github releases of %s: %w", repo, err)
	}
	best := ""
	for _, r := range list {
		m := tagRe.FindStringSubmatch(r.TagName)
		if r.Draft || m == nil {
			continue
		}
		if best == "" || util.CompareVersions(m[1], best) > 0 {
			best = m[1]
		}
	}
	if best == "" {
		return "", errors.New("no published release with a version tag in " + repo)
	}
	return best, nil
}
