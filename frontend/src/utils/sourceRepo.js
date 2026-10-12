// The repository the server build comes from, as the API reports it
// (source_repository): PatchMon/PatchMon for an official build, the fork for
// a fork build. Links to releases and to the code follow it.

export const UPSTREAM_REPO = "PatchMon/PatchMon";

const REPO_RE = /^[A-Za-z0-9][A-Za-z0-9-]{0,38}\/[A-Za-z0-9._-]{1,100}$/;

/**
 * Repository from an API payload, upstream when missing or malformed, so a
 * bad value can never become a link elsewhere than github.com.
 */
export function sourceRepo(info) {
	const repo = info?.source_repository;
	return typeof repo === "string" && REPO_RE.test(repo) && !repo.includes("..")
		? repo
		: UPSTREAM_REPO;
}

export const repoUrl = (repo) => `https://github.com/${repo}`;

export const isForkRepo = (repo) =>
	repo.toLowerCase() !== UPSTREAM_REPO.toLowerCase();

/**
 * Short commit stamped in the build, or null.
 */
export function shortCommit(info) {
	const sha = info?.source_commit;
	return typeof sha === "string" && /^[0-9a-f]{7,40}$/.test(sha)
		? sha.slice(0, 7)
		: null;
}
