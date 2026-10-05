import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { DashboardPage } from "../dashboards/page";
import { parseSearch, toSearchParams } from "../dashboards/search";

export const Route = createFileRoute("/dashboards/")({
  component: DashboardIndex,
  validateSearch: (raw: Record<string, unknown>) => toSearchParams(parseSearch(raw)),
});

function DashboardIndex() {
  const search = parseSearch(Route.useSearch() as Record<string, unknown>);
  const navigate = useNavigate();
  return <DashboardPage search={search} onSearch={() => undefined} onOpen={(dashboardId, replace) => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId }, search: toSearchParams(search), replace })} />;
}
