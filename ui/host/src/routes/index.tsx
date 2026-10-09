import { createFileRoute, Navigate } from "@tanstack/react-router";
import { useRuntimeStatus } from "../auth";

export const Route = createFileRoute("/")({
  component: RootNavigation,
});

function RootNavigation() {
  const { agent_available: agentAvailable } = useRuntimeStatus();
  if (!agentAvailable) return <Navigate to="/dashboards" replace />;
  return <Navigate to="/chat" replace />;
}
