import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import RoleHostAccess from "../../components/settings/RoleHostAccess";
import { hostScopeAPI } from "../../utils/api";

const toast = { success: vi.fn(), error: vi.fn() };
vi.mock("../../contexts/ToastContext", () => ({ useToast: () => toast }));

let scopeRoles = [];
let status = { available: true };
vi.mock("../../utils/api", () => ({
	hostGroupsAPI: {
		list: () =>
			Promise.resolve({
				data: [
					{ id: "g1", name: "Production" },
					{ id: "g2", name: "Web" },
				],
			}),
	},
	hostScopeAPI: {
		listRoles: vi.fn(() =>
			Promise.resolve({
				data: {
					roles: scopeRoles,
					status,
					incompatible_permissions: ["can_manage_users", "can_manage_settings"],
				},
			}),
		),
		updateRole: vi.fn(() => Promise.resolve({ data: {} })),
	},
}));

const renderFor = (role) =>
	render(
		<QueryClientProvider
			client={
				new QueryClient({ defaultOptions: { queries: { retry: false } } })
			}
		>
			<RoleHostAccess role={role} />
		</QueryClientProvider>,
	);

describe("RoleHostAccess", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		scopeRoles = [];
		status = { available: true };
	});

	it("never offers to limit the admin role", async () => {
		renderFor({ role: "admin", can_manage_users: true });
		expect(
			await screen.findByText("This role always has every host."),
		).toBeInTheDocument();
		expect(screen.queryByText("Every host")).toBeNull();
	});

	it("limits a role to the groups picked", async () => {
		renderFor({ role: "ops", can_view_hosts: true });
		fireEvent.click(
			await screen.findByLabelText("Only the hosts of some host groups"),
		);
		fireEvent.click(await screen.findByLabelText("Web"));
		fireEvent.click(screen.getByText("Save host access"));
		await waitFor(() =>
			expect(hostScopeAPI.updateRole).toHaveBeenCalledWith("ops", {
				limited: true,
				hostGroupIds: ["g2"],
			}),
		);
		expect(toast.success).toHaveBeenCalled();
	});

	it("shows the current limit and gives every host back", async () => {
		scopeRoles = [{ role: "ops", host_group_ids: ["g1"] }];
		renderFor({ role: "ops", can_view_hosts: true });
		expect(await screen.findByLabelText("Production")).toBeChecked();
		fireEvent.click(screen.getByLabelText("Every host"));
		fireEvent.click(screen.getByText("Save host access"));
		await waitFor(() =>
			expect(hostScopeAPI.updateRole).toHaveBeenCalledWith("ops", {
				limited: false,
				hostGroupIds: [],
			}),
		);
	});

	it("warns that no group means no host", async () => {
		scopeRoles = [{ role: "ops", host_group_ids: [] }];
		renderFor({ role: "ops", can_view_hosts: true });
		expect(
			await screen.findByText(
				"No group selected: users with this role will see no host.",
			),
		).toBeInTheDocument();
	});

	it("refuses to limit a role that can manage settings", async () => {
		renderFor({ role: "ops", can_manage_settings: true });
		const limited = await screen.findByLabelText(
			"Only the hosts of some host groups",
		);
		expect(limited).toBeDisabled();
		expect(
			screen.getByText(
				/cannot hold these permissions, which act on every host/,
			),
		).toBeInTheDocument();
	});

	it("explains when the server cannot limit roles", async () => {
		status = { available: false, reason: "the database role is missing" };
		renderFor({ role: "ops", can_view_hosts: true });
		expect(
			await screen.findByText(/the database role is missing/),
		).toBeInTheDocument();
		expect(
			screen.getByLabelText("Only the hosts of some host groups"),
		).toBeDisabled();
	});
});
