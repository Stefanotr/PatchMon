import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { Trans } from "react-i18next";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../contexts/ToastContext", () => ({
	useToast: () => ({ success: vi.fn(), warning: vi.fn(), error: vi.fn() }),
}));

import HostStatusPills from "../../components/HostStatusPills";
import RestartHostModal from "../../components/RestartHostModal";
import i18n, { currentLanguage, dateFnsLocale } from "../../i18n";
import { formatRelativeTime } from "../../utils/api";
import { describeReboot, restartChoiceError } from "../../utils/patchingApi";

describe("French UI", () => {
	beforeEach(() => i18n.changeLanguage("fr"));
	afterEach(() => i18n.changeLanguage("en"));

	it("resolves regional variants to French", async () => {
		await i18n.changeLanguage("fr-CA");
		expect(currentLanguage()).toBe("fr");
		expect(dateFnsLocale()?.code).toBe("fr");
		expect(document.documentElement.lang).toBe("fr");
	});

	it("renders the restart dialog in French", () => {
		render(
			<QueryClientProvider client={new QueryClient()}>
				<MemoryRouter>
					<RestartHostModal
						hostId="h1"
						hostName="win-ad1"
						isOpen
						onClose={vi.fn()}
						needsReboot={false}
					/>
				</MemoryRouter>
			</QueryClientProvider>,
		);
		expect(screen.getByText("Redémarrer l'hôte")).toBeInTheDocument();
		expect(
			screen.getByText(/ne signale aucun redémarrage en attente/),
		).toBeInTheDocument();
		expect(
			screen.getByRole("button", { name: "Planifier le redémarrage" }),
		).toBeInTheDocument();
	});

	it("renders the status pills in French", () => {
		render(
			<HostStatusPills
				host={{
					needs_reboot: true,
					last_update: new Date().toISOString(),
					securityUpdatesCount: 2,
				}}
			/>,
		);
		expect(screen.getByText("Redémarrage en attente")).toBeInTheDocument();
		expect(
			screen.getByText("Correctifs de sécurité requis"),
		).toBeInTheDocument();
	});

	it("formats relative times and restart texts in French", () => {
		const fiveMinAgo = new Date(Date.now() - 5 * 60 * 1000 - 1000);
		expect(formatRelativeTime(fiveMinAgo)).toBe("il y a 5 min");
		expect(formatRelativeTime(new Date(Date.now() - 2 * 86400000))).toBe(
			"il y a 2 jours",
		);
		expect(formatRelativeTime(new Date(Date.now() - 1000))).toBe("à l'instant");
		expect(describeReboot({ mode: "if_needed", delay_minutes: 15 })).toBe(
			"Redémarrer après l'exécution si nécessaire, 15 min après la fin",
		);
		expect(restartChoiceError({ mode: "always", when: "at", atTime: "" })).toBe(
			"Choisissez une heure",
		);
	});
});

describe("English UI", () => {
	it("keeps the English texts", () => {
		expect(currentLanguage()).toBe("en");
		expect(formatRelativeTime(new Date(Date.now() - 3 * 3600000 - 1000))).toBe(
			"3 hours ago",
		);
		expect(formatRelativeTime(new Date(Date.now() - 86400000 - 1000))).toBe(
			"1 day ago",
		);
		expect(formatRelativeTime(new Date(Date.now() + 10 * 60000 + 1000))).toBe(
			"in 10 min",
		);
	});
});

describe("<Trans> values", () => {
	it("renders a value holding markup as plain text", () => {
		const name = '<strong style="color:red">pwn</strong> & <img src=x>';
		const { container } = render(
			<p>
				<Trans
					i18nKey="Delete <strong>{{name}}</strong>?"
					values={{ name }}
					components={{ strong: <strong /> }}
				/>
			</p>,
		);
		expect(container.querySelectorAll("strong")).toHaveLength(1);
		expect(container.querySelector("img")).toBeNull();
		expect(container.querySelector("strong").textContent).toBe(name);
		expect(container.querySelector("[style]")).toBeNull();
	});
});
