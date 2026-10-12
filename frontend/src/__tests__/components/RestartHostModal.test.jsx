import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
