import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { DashboardPage } from "../dashboards/page";
import { parseSearch, toSearchParams, type DashboardSearch } from "../dashboards/search";

export const Route = createFileRoute("/dashboards/$dashboardId")({
  component: DashboardDetail,
  validateSearch: (raw: Record<string, unknown>) => toSearchParams(parseSearch(raw)),
});

function DashboardDetail() {
  const { dashboardId } = Route.useParams();
  const search = parseSearch(Route.useSearch() as Record<string, unknown>);
  const navigate = useNavigate();
  const go = (id: string, next: DashboardSearch, replace?: boolean) => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId: id }, search: toSearchParams(next), replace });
  return <DashboardPage dashboardId={dashboardId} search={search} onSearch={(next, replace) => go(dashboardId, next, replace)} onOpen={(id, replace) => go(id, {}, replace)} />;
}
