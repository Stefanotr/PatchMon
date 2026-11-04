import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import {
	isAgentUpdate,
	isOpen,
	isRunning,
	taskKey,
} from "../components/host/liveActivity";
import { dashboardAPI } from "../utils/api";

// What a host's agent is doing now (the live part of its activity), polled
// every LIVE_FAST_MS while something is going on, every LIVE_IDLE_MS
// otherwise.
export const LIVE_FAST_MS = 2000;
export const LIVE_IDLE_MS = 15000;

// After an agent update, the new version reaches the host record with the
// agent's first report, a moment after it reconnects.
const VERSION_REFRESH_DELAY_MS = 5000;

export const liveRefetchInterval = (tasks) =>
	Array.isArray(tasks) && tasks.some(isRunning) ? LIVE_FAST_MS : LIVE_IDLE_MS;

// useHostLiveActivity returns the host's live tasks, and its agent update in
// progress (null when none). When a task ends, the history and the host are
// refreshed.
export function useHostLiveActivity(hostId, { enabled = true } = {}) {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: ["host-live", hostId],
		queryFn: () =>
			dashboardAPI
				.getHostLiveActivity(hostId)
				.then((res) => (Array.isArray(res.data?.tasks) ? res.data.tasks : [])),
		enabled: Boolean(hostId) && enabled,
		refetchInterval: (q) => liveRefetchInterval(q.state.data),
		staleTime: 1000,
	});

	const openBefore = useRef(null);
	const versionTimer = useRef(null);
	// Another host (the page stays mounted): its tasks are not compared with
	// the last host's, and that host's pending refresh is dropped.
	// biome-ignore lint/correctness/useExhaustiveDependencies: runs again for each host on purpose
	useEffect(() => {
		openBefore.current = null;
		return () => clearTimeout(versionTimer.current);
	}, [hostId]);
	useEffect(() => {
		const tasks = query.data;
		if (!tasks) return;
		const open = new Set(tasks.filter(isOpen).map(taskKey));
		const before = openBefore.current;
		openBefore.current = open;
		if (!before || [...before].every((key) => open.has(key))) return;
		queryClient.invalidateQueries({ queryKey: ["host-activity", hostId] });
		queryClient.invalidateQueries({ queryKey: ["host", hostId] });
		clearTimeout(versionTimer.current);
		versionTimer.current = setTimeout(
			() => queryClient.invalidateQueries({ queryKey: ["host", hostId] }),
			VERSION_REFRESH_DELAY_MS,
		);
	}, [query.data, hostId, queryClient]);

	const tasks = query.data || [];
	// An update queued counts: one at a time.
	const agentUpdate = tasks.find((t) => isAgentUpdate(t) && isOpen(t)) || null;
	return {
		tasks,
		agentUpdate,
		isLoading: query.isLoading,
		error: query.error,
	};
}
