import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
	fireEvent,
	render,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import RolesTab from "../../components/settings/RolesTab";
import { permissionsAPI } from "../../utils/api";

const confirmMock = vi.fn(() => Promise.resolve(true));
const toast = { success: vi.fn(), error: vi.fn() };
vi.mock("../../contexts/AuthContext", () => ({
	useAuth: () => ({ refreshPermissions: vi.fn() }),
}));
vi.mock("../../contexts/ConfirmContext", () => ({
	useConfirm: () => confirmMock,
}));
vi.mock("../../contexts/ToastContext", () => ({ useToast: () => toast }));

const ROLES = [
	{ role: "readonly", can_view_dashboard: true, can_view_hosts: true },
	{ role: "ops_team", can_view_hosts: true, can_manage_hosts: false },
	{ role: "admin", can_view_dashboard: true, can_manage_settings: true },
];
vi.mock("../../utils/api", () => ({
	permissionsAPI: {
		getRoles: vi.fn(() => Promise.resolve({ data: ROLES })),
		updateRole: vi.fn(() => Promise.resolve({ data: {} })),
		deleteRole: vi.fn(() => Promise.resolve({ data: {} })),
	},
	hostScopeAPI: {
		listRoles: () =>
			Promise.resolve({
				data: {
					roles: [{ role: "ops_team", host_group_ids: ["g1"] }],
					status: { available: true },
					incompatible_permissions: [],
				},
			}),
	},
	hostGroupsAPI: { list: () => Promise.resolve({ data: [] }) },
}));

let location;
const LocationProbe = () => {
	location = useLocation();
	return null;
};

const renderAt = (url = "/settings/roles") =>
	render(
		<QueryClientProvider
			client={
				new QueryClient({ defaultOptions: { queries: { retry: false } } })
			}
		>
			<MemoryRouter initialEntries={[url]}>
				<RolesTab />
				<LocationProbe />
			</MemoryRouter>
		</QueryClientProvider>,
	);

const roleList = async () =>
	within(await screen.findByRole("navigation", { name: "Roles" }));

describe("RolesTab", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		global.fetch = vi.fn(() =>
			Promise.resolve({ ok: true, json: () => ({ enabled: false }) }),
		);
	});
	afterEach(() => {
		delete global.fetch;
	});

	it("lists every role, built-in ones first, and opens the first one", async () => {
		renderAt();
		const list = await roleList();
		const names = list
			.getAllByRole("button")
			.map((b) => b.querySelector(".truncate").textContent);
		expect(names).toEqual(["admin", "readonly", "ops team"]);
		expect(
			screen.getByRole("heading", { level: 3, name: "admin" }),
		).toBeInTheDocument();
		expect(list.getByTitle("Some host groups only")).toBeInTheDocument();
		expect(list.getAllByTitle("Built-in Role")).toHaveLength(1);
	});

	it("keeps built-in roles read-only", async () => {
		renderAt();
		expect(
			await screen.findByText(/This built-in role keeps its permissions/),
		).toBeInTheDocument();
		expect(screen.getByLabelText("View Dashboard")).toBeDisabled();
		expect(screen.queryByText("Delete role")).toBeNull();
	});

	it("saves a change from the save bar", async () => {
		renderAt("/settings/roles?role=ops_team");
		fireEvent.click(await screen.findByLabelText("View Dashboard"));
		expect(screen.getByText("Unsaved changes to this role")).toBeVisible();
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() =>
			expect(permissionsAPI.updateRole).toHaveBeenCalledWith(
				"ops_team",
				expect.objectContaining({
					can_view_dashboard: true,
					can_view_hosts: true,
				}),
			),
		);
		expect(toast.success).toHaveBeenCalled();
	});

	it("asks before dropping unsaved changes", async () => {
		confirmMock.mockResolvedValueOnce(false);
		renderAt("/settings/roles?role=ops_team");
		fireEvent.click(await screen.findByLabelText("View Dashboard"));
		const list = await roleList();
		fireEvent.click(list.getByText("readonly"));
		await waitFor(() => expect(confirmMock).toHaveBeenCalled());
		expect(new URLSearchParams(location.search).get("role")).toBe("ops_team");
		expect(screen.getByLabelText("View Dashboard")).toBeChecked();

		fireEvent.click(list.getByText("readonly"));
		await waitFor(() =>
			expect(new URLSearchParams(location.search).get("role")).toBe("readonly"),
		);
	});

	it("selects a whole group at once", async () => {
		renderAt("/settings/roles?role=ops_team");
		await screen.findByLabelText("View Dashboard");
		fireEvent.click(screen.getAllByRole("button", { name: "Select all" })[0]);
		expect(screen.getByLabelText("View Dashboard")).toBeChecked();
		expect(screen.getByLabelText("View Hosts")).toBeChecked();
	});

	it("compares roles and opens one from the table", async () => {
		renderAt("/settings/roles?view=compare");
		const header = await screen.findByRole("columnheader", {
			name: /readonly/,
		});
		expect(screen.getAllByRole("columnheader")).toHaveLength(4);
		fireEvent.click(within(header).getByRole("button"));
		await waitFor(() =>
			expect(
				screen.getByRole("heading", { level: 3, name: "readonly" }),
			).toBeInTheDocument(),
		);
		expect(new URLSearchParams(location.search).get("view")).toBeNull();
	});
});
