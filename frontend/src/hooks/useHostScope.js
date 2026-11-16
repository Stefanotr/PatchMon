import { useQuery } from "@tanstack/react-query";
import { hostScopeAPI } from "../utils/api";

// Whether the signed-in user's role is limited to some host groups. The
// server enforces the limit everywhere; this only hides what such a user
// cannot do (add hosts, manage host groups and patch policies...).
export function useHostScope() {
	const { data } = useQuery({
		queryKey: ["host-scope", "me"],
		queryFn: () => hostScopeAPI.getMine().then((res) => res.data),
		staleTime: 60 * 1000,
	});
	return {
		limited: data?.limited === true,
		hostGroupIds: data?.host_group_ids ?? [],
	};
}
