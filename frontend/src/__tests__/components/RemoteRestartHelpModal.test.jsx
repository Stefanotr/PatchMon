import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import RemoteRestartHelpModal from "../../components/RemoteRestartHelpModal";

const renderModal = (props) => {
	const queryClient = new QueryClient();
	const invalidate = vi.spyOn(queryClient, "invalidateQueries");
	const onClose = vi.fn();
	render(
		<QueryClientProvider client={queryClient}>
			<RemoteRestartHelpModal isOpen onClose={onClose} {...props} />
		</QueryClientProvider>,
	);
	return { invalidate, onClose };
};

describe("RemoteRestartHelpModal", () => {
	it("shows the command of the host's platform first", () => {
		renderModal({
			host: { friendly_name: "win-ad1", os_type: "Windows Server 2025" },
		});
		expect(screen.getByText("win-ad1")).toBeInTheDocument();
		expect(screen.getByRole("tab", { name: "Windows" })).toHaveAttribute(
			"aria-selected",
			"true",
		);
		expect(
			screen.getByText(/Restart-Service PatchMonAgent/),
		).toBeInTheDocument();
	});

	it("switches platform and copies the command shown", async () => {
		const writeText = vi.fn().mockResolvedValue();
		Object.assign(navigator, { clipboard: { writeText } });
		renderModal({ host: { os_type: "Ubuntu" } });
		fireEvent.click(screen.getByRole("tab", { name: "FreeBSD" }));
		fireEvent.click(screen.getByTitle("Copy the command"));
		await waitFor(() =>
			expect(writeText).toHaveBeenCalledWith(
				"/usr/local/bin/patchmon-agent config remote-restart on && service patchmon_agent restart",
			),
		);
		expect(await screen.findByText("Copied")).toBeInTheDocument();
	});

	it("refreshes the hosts' restart setting when closed", () => {
		const { invalidate, onClose } = renderModal({});
		fireEvent.click(screen.getAllByRole("button", { name: "Close" })[0]);
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["remote-restart"] });
		expect(onClose).toHaveBeenCalled();
	});
});
