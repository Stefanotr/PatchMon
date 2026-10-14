import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import LiveActivityPanel from "../../components/host/LiveActivityPanel";
import {
	agentUpdatePillText,
	agentUpdateSteps,
	formatElapsed,
	jobStatusText,
} from "../../components/host/liveActivity";
import {
	LIVE_FAST_MS,
	LIVE_IDLE_MS,
	liveRefetchInterval,
} from "../../hooks/useHostLiveActivity";

const ago = (s) => new Date(Date.now() - s * 1000).toISOString();

const update = (over = {}) => ({
	id: "job-1",
	kind: "job",
	name: "update_agent",
	status: "active",
	finished: false,
	stage: "downloading",
	percent: 40,
	from_version: "1.4.0",
	to_version: "1.5.0",
	trigger: "server",
	stages: { sent: ago(10), downloading: ago(8) },
	started_at: ago(10),
	...over,
});

const renderPanel = (tasks) =>
	render(
		<MemoryRouter>
			<LiveActivityPanel tasks={tasks} />
		</MemoryRouter>,
	);

const stepStates = () =>
	within(screen.getByTestId("agent-update-steps"))
		.getAllByRole("listitem")
		.map((li) => `${li.textContent}:${li.dataset.state}`);

describe("agentUpdateSteps", () => {
	it("marks the steps done up to the current one", () => {
		expect(agentUpdateSteps(update()).map((s) => s.state)).toEqual([
			"done",
			"current",
			"pending",
			"pending",
			"pending",
			"pending",
		]);
	});

	it("keeps the agent checking on the first step", () => {
		const steps = agentUpdateSteps(update({ stage: "checking" }));
		expect(steps[0].state).toBe("current");
		expect(steps[1].state).toBe("pending");
	});

	it("puts the failure on the last step reached", () => {
		const steps = agentUpdateSteps(
			update({
				stage: "failed",
				stages: { sent: ago(9), downloading: ago(8), verifying: ago(5) },
			}),
		);
		expect(steps.map((s) => s.state)).toEqual([
			"done",
			"done",
			"error",
			"pending",
			"pending",
			"pending",
		]);
	});

	it("has no sent step for an update the agent started", () => {
		const steps = agentUpdateSteps(update({ trigger: "agent" }));
		expect(steps.map((s) => s.stage)).not.toContain("sent");
		expect(steps[0].state).toBe("current");
	});

	it("shows an update closed for want of news as failed, not running", () => {
		const steps = agentUpdateSteps(
			update({ status: "failed", finished: true, stage: "downloading" }),
		);
		expect(steps.map((s) => s.state)).toEqual([
			"done",
			"error",
			"pending",
			"pending",
			"pending",
			"pending",
		]);
	});

	it("completes every step once the agent is back", () => {
		expect(
			agentUpdateSteps(update({ stage: "done" })).every(
				(s) => s.state === "done",
			),
		).toBe(true);
	});
});

describe("LiveActivityPanel", () => {
	it("shows an agent update step by step with its download", () => {
		renderPanel([update()]);
		expect(screen.getByText("Agent update")).toBeInTheDocument();
		expect(screen.getByText("1 task in progress")).toBeInTheDocument();
		expect(stepStates()).toEqual([
			"Sent:done",
			"Download:current",
			"Verification:pending",
			"Installation:pending",
			"Agent restart:pending",
			"Back online:pending",
		]);
		expect(screen.getByRole("progressbar")).toHaveAttribute(
			"aria-valuenow",
			"40",
		);
		expect(
			screen.getByText(/Downloading the new agent \(40%\)/),
		).toBeInTheDocument();
		expect(screen.getByText(/for \d+ s/)).toBeInTheDocument();
	});

	it("says when an older agent gives no news", () => {
		renderPanel([
			update({ stage: "sent", percent: null, started_at: ago(90) }),
		]);
		expect(screen.getByText(/No news from the agent yet/)).toBeInTheDocument();
	});

	it("shows how an update ended", () => {
		renderPanel([
			update({
				status: "completed",
				finished: true,
				stage: "done",
				percent: null,
				finished_at: ago(5),
			}),
		]);
		expect(
			screen.getByText("Updated to v1.5.0, the agent is back online"),
		).toBeInTheDocument();
		expect(screen.getByText(/ended \d+ s ago/)).toBeInTheDocument();
		expect(screen.getByText("Live")).toBeInTheDocument();
	});

	it("shows a postponed update without its steps", () => {
		renderPanel([
			update({
				status: "failed",
				finished: true,
				stage: "skipped",
				error: "update skipped: a patch run is in progress",
				finished_at: ago(3),
			}),
		]);
		expect(screen.queryByTestId("agent-update-steps")).toBeNull();
		expect(screen.getByText("Postponed by the agent")).toBeInTheDocument();
		expect(
			screen.getByText("update skipped: a patch run is in progress"),
		).toBeInTheDocument();
	});

	it("shows a patch run with its last output line and a link", () => {
		renderPanel([
			{
				id: "run-1",
				kind: "patch_run",
				name: "patch_package",
				package: "openssl",
				status: "running",
				finished: false,
				message: "Unpacking libssl3 ...",
				started_at: ago(30),
			},
		]);
		expect(screen.getByText("Patch openssl")).toBeInTheDocument();
		expect(screen.getByText("Unpacking libssl3 ...")).toBeInTheDocument();
		expect(screen.getByRole("link", { name: /Open the run/ })).toHaveAttribute(
			"href",
			"/patching/runs/run-1",
		);
	});

	it("shows a compliance scan and a queued job", () => {
		renderPanel([
			{
				id: "compliance-scan",
				kind: "compliance_scan",
				name: "CIS Level 1",
				status: "active",
				finished: false,
				stage: "evaluating",
				percent: 15,
				started_at: ago(60),
			},
			{
				id: "q-1",
				kind: "queued",
				name: "report_now",
				status: "waiting",
				finished: false,
				started_at: ago(2),
			},
		]);
		expect(screen.getByText("Compliance scan")).toBeInTheDocument();
		expect(
			screen.getByText(/Evaluating the rules \(15%\)/),
		).toBeInTheDocument();
		expect(screen.getByText("Report Now")).toBeInTheDocument();
		expect(screen.getByText("Queued")).toBeInTheDocument();
		expect(screen.getByText("2 tasks in progress")).toBeInTheDocument();
	});

	it("shows a task scheduled for later without a spinner", () => {
		renderPanel([
			{
				id: "q-9",
				kind: "queued",
				name: "run_scan",
				status: "delayed",
				finished: false,
				running: false,
				scheduled_at: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
			},
		]);
		expect(screen.getByText("Scheduled")).toBeInTheDocument();
		expect(screen.getByText(/planned in \d+ min/)).toBeInTheDocument();
		expect(screen.getByText("Live")).toBeInTheDocument();
		expect(document.querySelector(".animate-spin")).toBeNull();
	});

	it("says when nothing is going on", () => {
		renderPanel([]);
		expect(
			screen.getByText(/Nothing in progress on this host/),
		).toBeInTheDocument();
	});
});

describe("live activity helpers", () => {
	it("polls fast only while a task is open", () => {
		expect(liveRefetchInterval([update()])).toBe(LIVE_FAST_MS);
		expect(liveRefetchInterval([update({ finished: true })])).toBe(
			LIVE_IDLE_MS,
		);
		expect(liveRefetchInterval(undefined)).toBe(LIVE_IDLE_MS);
		// Scheduled for later: shown, but no reason to poll fast.
		expect(
			liveRefetchInterval([update({ running: false, status: "delayed" })]),
		).toBe(LIVE_IDLE_MS);
	});

	it("words the header pill", () => {
		expect(agentUpdatePillText(update())).toBe("Agent update: downloading 40%");
		expect(agentUpdatePillText(update({ stage: "restarting" }))).toBe(
			"Agent update: restarting",
		);
		expect(agentUpdatePillText({ kind: "queued", name: "update_agent" })).toBe(
			"Agent update queued",
		);
	});

	it("words a job waiting differently from a task not picked up", () => {
		expect(jobStatusText({ kind: "job", status: "waiting" })).toBe(
			"Waiting for the agent's answer",
		);
		expect(jobStatusText({ kind: "queued", status: "waiting" })).toBe("Queued");
		expect(
			jobStatusText({ kind: "queued", status: "delayed", attempt: 2 }),
		).toBe("Retry scheduled");
	});

	it("formats elapsed time", () => {
		expect(formatElapsed(12_400)).toBe("12 s");
		expect(formatElapsed(185_000)).toBe("3 min 5 s");
		expect(formatElapsed(3_720_000)).toBe("1 h 2 min");
	});
});
