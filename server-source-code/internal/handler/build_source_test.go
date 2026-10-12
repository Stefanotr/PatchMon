package handler

import (
	"testing"

	"github.com/PatchMon/PatchMon/server-source-code/internal/forksource"
)

func TestWithBuildSource(t *testing.T) {
	oldRepo, oldCommit := forksource.Repository, forksource.Commit
	t.Cleanup(func() { forksource.Repository, forksource.Commit = oldRepo, oldCommit })

	forksource.Repository, forksource.Commit = "", ""
	m := withBuildSource(map[string]interface{}{"version": "2.1.3"})
	if m["source_repository"] != forksource.Upstream || m["is_fork"] != false || m["version"] != "2.1.3" {
		t.Fatalf("official build: %v", m)
	}
	if _, ok := m["source_commit"]; ok {
		t.Fatalf("unstamped commit reported: %v", m)
	}

	forksource.Repository, forksource.Commit = "Stefanotr/PatchMon", "abcdef1234567"
	m = withBuildSource(map[string]interface{}{})
	if m["source_repository"] != "Stefanotr/PatchMon" || m["is_fork"] != true ||
		m["source_url"] != "https://github.com/Stefanotr/PatchMon" ||
		m["source_commit_url"] != "https://github.com/Stefanotr/PatchMon/commit/abcdef1234567" {
		t.Fatalf("fork build: %v", m)
	}
}
