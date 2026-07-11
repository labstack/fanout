import { useQuery } from "@tanstack/react-query";
import { api } from "@/shared/lib/api-client";
import { keys } from "@/shared/lib/query-keys";
import { useNamespace } from "@/shared/hooks/use-namespace";
import { overviewSchema } from "@/shared/lib/schemas/overview";

export function useOverview(window = 60) {
  const namespace = useNamespace();
  return useQuery({
    queryKey: keys.overview(window, namespace),
    queryFn: () => {
      const params = new URLSearchParams({ window: String(window) });
      if (namespace) params.set("namespace", namespace);
      return api(`/api/overview?${params}`, overviewSchema);
    },
  });
}
