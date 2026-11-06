import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { SearchPage, logsPreset } from "../explore/search-page";
import { parseExploreSearch, toExploreParams } from "../explore/search";

export const Route = createFileRoute("/logs")({
  component: Logs,
  validateSearch: (raw: Record<string, unknown>) => toExploreParams(parseExploreSearch(raw)),
});

function Logs() {
  const search = parseExploreSearch(Route.useSearch() as Record<string, unknown>);
  const navigate = useNavigate();
  return <SearchPage key="logs" preset={logsPreset} search={search} onSearch={(next, replace) => void navigate({ to: "/logs", search: toExploreParams(next), replace })}
    onSaved={dashboardId => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId } })} />;
}
