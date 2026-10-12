import { describe, expect, it } from "vitest";
import {
	isForkRepo,
	repoUrl,
	shortCommit,
	sourceRepo,
	UPSTREAM_REPO,
} from "../../utils/sourceRepo";

describe("source repository", () => {
	it("follows the build's repository", () => {
		const repo = sourceRepo({ source_repository: "Stefanotr/PatchMon" });
		expect(repo).toBe("Stefanotr/PatchMon");
		expect(isForkRepo(repo)).toBe(true);
		expect(repoUrl(repo)).toBe("https://github.com/Stefanotr/PatchMon");
	});

	it("falls back to upstream on a missing or malformed value", () => {
		for (const info of [
			null,
			{},
			{ source_repository: "" },
			{ source_repository: "evil.example/../x" },
			{ source_repository: "javascript:alert(1)" },
			{ source_repository: "a/b/c" },
			{ source_repository: 42 },
		]) {
			expect(sourceRepo(info)).toBe(UPSTREAM_REPO);
		}
		expect(isForkRepo(UPSTREAM_REPO)).toBe(false);
		expect(isForkRepo("patchmon/patchmon")).toBe(false);
	});

	it("shortens a stamped commit and ignores anything else", () => {
		expect(shortCommit({ source_commit: "0123456789abcdef" })).toBe("0123456");
		expect(shortCommit({ source_commit: "zzz" })).toBeNull();
		expect(shortCommit({})).toBeNull();
	});
});
