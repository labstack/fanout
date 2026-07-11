import { useQuery } from "@tanstack/react-query";
import { api } from "@/shared/lib/api-client";
import { keys } from "@/shared/lib/query-keys";
import { useNamespace } from "@/shared/hooks/use-namespace";
import { overviewSchema } from "@/shared/lib/schemas/overview";

export function useOverview(windowSecs = 60) {
  const namespace = useNamespace();
  return useQuery({
    queryKey: keys.overview(windowSecs, namespace),
    queryFn: () => {
      const params = new URLSearchParams({ window: String(windowSecs) });
      if (namespace) params.set("namespace", namespace);
      return api(`/api/overview?${params}`, overviewSchema);
    },
  });
}
