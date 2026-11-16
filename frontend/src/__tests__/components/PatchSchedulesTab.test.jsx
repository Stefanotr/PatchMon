import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import PatchSchedulesTab from "../../components/patching/PatchSchedulesTab";
import { patchingAPI } from "../../utils/patchingApi";

let canManage = true;
const confirmMock = vi.fn(() => Promise.resolve(true));
const toast = { success: vi.fn(), error: vi.fn() };

vi.mock("../../contexts/AuthContext", () => ({
	useAuth: () => ({ canManagePatching: () => canManage }),
}));
vi.mock("../../contexts/ConfirmContext", () => ({
	useConfirm: () => confirmMock,
}));
vi.mock("../../contexts/ToastContext", () => ({ useToast: () => toast }));
vi.mock("../../utils/api", () => ({
	formatDate: (d) => `date:${d}`,
	hostGroupsAPI: {
		list: () => Promise.resolve({ data: [{ id: "g1", name: "Production" }] }),
	},
}));
vi.mock("../../utils/patchingApi", () => ({
	patchingAPI: {
		getSchedules: vi.fn(),
		createSchedule: vi.fn(() => Promise.resolve({})),
		updateSchedule: vi.fn(() => Promise.resolve({})),
		deleteSchedule: vi.fn(() => Promise.resolve({})),
		runSchedule: vi.fn(() =>
			Promise.resolve({ result: { queued: 2, up_to_date: 1 } }),
		),
	},
}));

const SCHEDULES = [
	{
		id: "s1",
		name: "Prod security",
		host_group_id: "g1",
		host_group_name: "Production",
		scope: "security",
		frequency: "weekly",
		weekdays: [3],
		month_week: 0,
		month_weekday: 0,
		time_of_day: "03:00",
		enabled: true,
		next_run_at: "2026-11-11T02:00:00Z",
		last_run_at: "2026-11-04T02:00:00Z",
		last_result: { queued: 4, up_to_date: 1, busy: 1 },
	},
	{
		id: "s2",
		name: "Everything monthly",
		host_group_id: null,
		host_group_name: null,
		scope: "all",
		frequency: "monthly",
		weekdays: [],
		month_week: 5,
		month_weekday: 5,
		time_of_day: "22:00",
		enabled: false,
		next_run_at: null,
		last_run_at: null,
		last_result: null,
	},
];

const renderTab = () =>
	render(
		<QueryClientProvider
			client={
				new QueryClient({ defaultOptions: { queries: { retry: false } } })
			}
		>
			<MemoryRouter>
				<PatchSchedulesTab />
			</MemoryRouter>
		</QueryClientProvider>,
	);

describe("PatchSchedulesTab", () => {
	beforeEach(() => {
		canManage = true;
		vi.clearAllMocks();
		patchingAPI.getSchedules.mockResolvedValue({
			schedules: SCHEDULES,
			timezone: "Europe/Paris",
		});
	});

	it("lists schedules with when they run and what the last run did", async () => {
		renderTab();
		expect(await screen.findByText("Prod security")).toBeInTheDocument();
		expect(
			screen.getByText("Weekly on Wednesday at 03:00"),
		).toBeInTheDocument();
		expect(
			screen.getByText("Last Friday of the month at 22:00"),
		).toBeInTheDocument();
		expect(screen.getByText("All hosts")).toBeInTheDocument();
		expect(
			screen.getByText("4 dry runs queued, 1 host up to date, 1 host busy"),
		).toBeInTheDocument();
		expect(screen.getByText(/Times are in Europe\/Paris/)).toBeInTheDocument();
	});

	it("runs a schedule now after confirmation", async () => {
		renderTab();
		const run = await screen.findAllByTitle("Run now");
		fireEvent.click(run[0]);
		await waitFor(() =>
			expect(patchingAPI.runSchedule).toHaveBeenCalledWith("s1"),
		);
		expect(confirmMock).toHaveBeenCalled();
		await waitFor(() =>
			expect(toast.success).toHaveBeenCalledWith(
				"2 dry runs queued, 1 host up to date",
			),
		);
	});

	it("turns a schedule on with its settings unchanged", async () => {
		renderTab();
		await screen.findByText("Everything monthly");
		const switches = screen.getAllByRole("switch");
		fireEvent.click(switches[1]);
		await waitFor(() =>
			expect(patchingAPI.updateSchedule).toHaveBeenCalledWith("s2", {
				name: "Everything monthly",
				host_group_id: null,
				scope: "all",
				frequency: "monthly",
				weekdays: [],
				month_week: 5,
				month_weekday: 5,
				time_of_day: "22:00",
				enabled: true,
			}),
		);
	});

	it("creates a weekly schedule from the form", async () => {
		renderTab();
		await screen.findByText("Prod security");
		fireEvent.click(screen.getByText("Create schedule"));
		fireEvent.change(screen.getByLabelText("Name"), {
			target: { value: "Nightly" },
		});
		fireEvent.click(screen.getByText("Save"));
		await waitFor(() =>
			expect(patchingAPI.createSchedule).toHaveBeenCalledWith({
				name: "Nightly",
				host_group_id: null,
				scope: "security",
				frequency: "weekly",
				weekdays: [3],
				month_week: 0,
				month_weekday: 0,
				time_of_day: "03:00",
				enabled: true,
			}),
		);
	});

	it("shows no write action to a user who cannot manage patching", async () => {
		canManage = false;
		renderTab();
		await screen.findByText("Prod security");
		expect(screen.queryByText("Create schedule")).toBeNull();
		expect(screen.queryAllByTitle("Run now")).toHaveLength(0);
		for (const sw of screen.getAllByRole("switch")) {
			expect(sw).toBeDisabled();
		}
	});
});
