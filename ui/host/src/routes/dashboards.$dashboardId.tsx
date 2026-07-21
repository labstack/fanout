import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useFanoutApp } from "../App";
import Dashboard from "../dashboard";

export const Route = createFileRoute("/dashboards/$dashboardId")({
  component: DashboardDetail,
});

function DashboardDetail() {
  const { dashboardId } = Route.useParams();
  const navigate = useNavigate();
  const { openChat } = useFanoutApp();
  return <Dashboard dashboardID={dashboardId} onOpenChat={openChat} onDashboardChange={(nextID, replace) => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId: nextID }, replace })} />;
}
