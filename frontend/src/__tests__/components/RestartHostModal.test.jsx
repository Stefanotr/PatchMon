import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	act,
	fireEvent,
	render,
	screen,
	waitFor,
} from "@testing-library/react";
import { useState } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const toast = vi.hoisted(() => ({
	success: vi.fn(),
	warning: vi.fn(),
	error: vi.fn(),
	info: vi.fn(),
}));

vi.mock("../../contexts/ToastContext", () => ({ useToast: () => toast }));

vi.mock("../../utils/api", () => ({
	adminHostsAPI: { rebootHost: vi.fn(), cancelHostReboot: vi.fn() },
	formatRelativeTime: () => "just now",
}));

import HostStatusPills from "../../components/HostStatusPills";
import RestartHostModal, {
	restartHistoryPath,
	toastDuration,
} from "../../components/RestartHostModal";
import { adminHostsAPI } from "../../utils/api";

const renderModal = (props) =>
	render(
		<QueryClientProvider client={new QueryClient()}>
			<MemoryRouter>
				<RestartHostModal
					hostId="h1"
					hostName="win-ad1"
					isOpen
					onClose={vi.fn()}
					{...props}
				/>
			</MemoryRouter>
		</QueryClientProvider>,
	);

describe("RestartHostModal", () => {
	beforeEach(() => vi.clearAllMocks());

	it("says a restart is optional when none is pending", () => {
		renderModal({ needsReboot: false });
		expect(
			screen.getByText(/does not report a pending restart/),
		).toBeInTheDocument();
		expect(screen.queryByText(/A restart is pending/)).not.toBeInTheDocument();
	});

	it("shows why a restart is pending", () => {
		renderModal({
			needsReboot: true,
			rebootReason: "Windows Update requires a restart",
		});
		expect(
			screen.getByText(/A restart is pending on this host: Windows Update/),
		).toBeInTheDocument();
		expect(
			screen.queryByText(/does not report a pending restart/),
		).not.toBeInTheDocument();
	});

	it("says nothing when the state is unknown", () => {
		renderModal({});
		expect(
			screen.queryByText(/does not report a pending restart/),
		).not.toBeInTheDocument();
		expect(screen.queryByText(/A restart is pending/)).not.toBeInTheDocument();
	});

	it("links to the restart history and still schedules", async () => {
		adminHostsAPI.rebootHost.mockResolvedValue({
			status: 200,
			data: { message: "Restart scheduled" },
		});
		renderModal({ needsReboot: false });
		expect(
			screen.getByRole("link", { name: /Restart history/ }),
		).toHaveAttribute("href", restartHistoryPath("h1"));
		fireEvent.click(screen.getByRole("button", { name: "Schedule restart" }));
		await waitFor(() => expect(adminHostsAPI.rebootHost).toHaveBeenCalled());
		expect(adminHostsAPI.rebootHost.mock.calls[0][1]).toMatchObject({
			mode: "always",
		});
	});
});

// The dialog as the Hosts page uses it: one instance, keyed by the host.
function HostsPageDialog() {
	const [host, setHost] = useState(null);
	return (
		<>
			<button type="button" onClick={() => setHost({ id: "a", name: "alpha" })}>
				open alpha
			</button>
			<button type="button" onClick={() => setHost({ id: "b", name: "bravo" })}>
				open bravo
			</button>
			<RestartHostModal
				key={host?.id}
				hostId={host?.id}
				hostName={host?.name}
				isOpen={!!host}
				onClose={() => setHost(null)}
			/>
		</>
	);
}

const renderHostsPage = () =>
	render(
		<QueryClientProvider client={new QueryClient()}>
			<MemoryRouter>
				<HostsPageDialog />
			</MemoryRouter>
		</QueryClientProvider>,
	);

describe("RestartHostModal across hosts", () => {
	beforeEach(() => vi.clearAllMocks());

	it("starts afresh for the next host while a command is still pending", async () => {
		let answerAlpha;
		adminHostsAPI.rebootHost.mockImplementationOnce(
			() =>
				new Promise((resolve) => {
					answerAlpha = resolve;
				}),
		);
		renderHostsPage();

		fireEvent.click(screen.getByRole("button", { name: "open alpha" }));
		fireEvent.click(screen.getByRole("button", { name: "Schedule restart" }));
		expect(
			await screen.findByRole("button", { name: "Sending..." }),
		).toBeDisabled();

		fireEvent.click(screen.getByRole("button", { name: "Close" }));
		fireEvent.click(screen.getByRole("button", { name: "open bravo" }));
		expect(screen.getByText("bravo")).toBeInTheDocument();
		expect(
			screen.queryByRole("button", { name: "Sending..." }),
		).not.toBeInTheDocument();
		expect(
			screen.getByRole("button", { name: "Schedule restart" }),
		).not.toBeDisabled();

		// Alpha's answer names alpha and leaves bravo's dialog open.
		await act(async () => {
			answerAlpha({ status: 200, data: { message: "Restart scheduled" } });
		});
		await waitFor(() =>
			expect(toast.success).toHaveBeenCalledWith(
				"alpha: Restart scheduled",
				expect.any(Number),
			),
		);
		expect(screen.getByText("bravo")).toBeInTheDocument();
	});

	it("shows an agent's refusal as it comes, long enough to read", async () => {
		const reason =
			"The agent refused: remote restarts are disabled on this host: set allow_remote_reboot: true in /etc/patchmon/config.yml to allow them";
		adminHostsAPI.rebootHost.mockRejectedValueOnce({
			response: { status: 409, data: { error: reason } },
		});
		renderModal({});
		fireEvent.click(screen.getByRole("button", { name: "Schedule restart" }));
		await waitFor(() =>
			expect(toast.error).toHaveBeenCalledWith(reason, toastDuration(reason)),
		);
		expect(toastDuration(reason)).toBeGreaterThan(5000);
	});
});

describe("toast duration", () => {
	it("grows with the text, between 5 and 15 seconds", () => {
		expect(toastDuration("Restart scheduled")).toBe(5000);
		expect(toastDuration("x".repeat(100))).toBe(7500);
		expect(toastDuration("x".repeat(1000))).toBe(15000);
		expect(toastDuration(undefined)).toBe(5000);
	});
});

describe("restart history path", () => {
	it("opens the activity tab filtered on restarts", () => {
		expect(restartHistoryPath("a b")).toBe(
			"/hosts/a%20b?tab=activity&type=reboot,reboot_cancel&since=30d",
		);
	});
});

describe("HostStatusPills reboot pill", () => {
	const host = {
		needs_reboot: true,
		reboot_reason: "Kernel update",
		last_update: new Date().toISOString(),
	};

	it("opens the restart dialog when clickable", () => {
		const onRebootClick = vi.fn();
		render(<HostStatusPills host={host} onRebootClick={onRebootClick} />);
		fireEvent.click(
			screen.getByRole("button", { name: "Reboot pending: restart the host" }),
		);
		expect(onRebootClick).toHaveBeenCalledTimes(1);
	});

	it("stays informational without a handler", () => {
		render(<HostStatusPills host={host} />);
		expect(
			screen.getByRole("button", { name: "Reboot pending" }),
		).toBeInTheDocument();
	});
});
