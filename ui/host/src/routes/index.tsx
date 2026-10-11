import { createFileRoute, Navigate } from "@tanstack/react-router";

export const Route = createFileRoute("/")({
  component: RootNavigation,
});

function RootNavigation() {
  return <Navigate to="/dashboards" replace />;
}
