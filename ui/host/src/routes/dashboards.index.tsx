import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useFanoutApp } from "../App";
import Dashboard from "../dashboard";

export const Route = createFileRoute("/dashboards/")({
  component: DashboardIndex,
});

function DashboardIndex() {
  const navigate = useNavigate();
  const { openChat } = useFanoutApp();
  return <Dashboard onOpenChat={openChat} onDashboardChange={(dashboardId) => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId }, replace: true })} />;
}
