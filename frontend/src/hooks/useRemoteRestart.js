import { useQuery } from "@tanstack/react-query";
import { tk } from "../i18n";
import { adminHostsAPI } from "../utils/api";

// Whether hosts allow remote restarts (remote-restart-enabled in their
// agent's config.yml), as each agent last said on connecting. Only `false`
// greys a restart out: `true` and an unknown answer (an older agent) leave
// it to the agent, which refuses on its own when restarts are off.

const STALE_MS = 30 * 1000;

// Shown where a restart is greyed out.
export const REMOTE_RESTART_DISABLED_HINT = tk(
	"Remote restarts are disabled on this host. To allow them, set remote-restart-enabled to true under integrations in the agent's config.yml, then restart the agent.",
);

// useHostRemoteRestart returns true, false or null (not known) for one host.
export function useHostRemoteRestart(hostId, enabled = true) {
	const { data } = useQuery({
		queryKey: ["remote-restart", "host", hostId],
		queryFn: () =>
			adminHostsAPI
				.getRemoteRestart(hostId)
				.then((res) => res.data?.enabled ?? null),
		enabled: Boolean(hostId) && enabled,
		staleTime: STALE_MS,
	});
	return data ?? null;
}

// useRemoteRestartMap returns { [hostId]: true | false } for the hosts whose
// agent said.
export function useRemoteRestartMap(enabled = true) {
	const { data } = useQuery({
		queryKey: ["remote-restart", "all"],
		queryFn: () =>
			adminHostsAPI.listRemoteRestart().then((res) => res.data?.hosts || {}),
		enabled,
		staleTime: STALE_MS,
	});
	return data || {};
}
