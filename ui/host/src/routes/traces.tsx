import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { SearchPage, tracesPreset } from "../explore/search-page";
import { parseExploreSearch, toExploreParams } from "../explore/search";

export const Route = createFileRoute("/traces")({
  component: Traces,
  validateSearch: (raw: Record<string, unknown>) => toExploreParams(parseExploreSearch(raw)),
});

function Traces() {
  const search = parseExploreSearch(Route.useSearch() as Record<string, unknown>);
  const navigate = useNavigate();
  return <SearchPage key="traces" preset={tracesPreset} search={search} onSearch={(next, replace) => void navigate({ to: "/traces", search: toExploreParams(next), replace })}
    onSaved={dashboardId => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId } })} />;
}
