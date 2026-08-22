import { createFileRoute, Navigate } from "@tanstack/react-router";
import { useRuntimeStatus } from "../auth";

export const Route = createFileRoute("/")({
  component: LegacyChatRedirect,
});

function LegacyChatRedirect() {
  const { agent_available: agentAvailable } = useRuntimeStatus();
  if (!agentAvailable) return <Navigate to="/dashboards" replace />;
  const threadID = localStorage.getItem("fanout.thread-id");
  if (threadID) {
    localStorage.removeItem("fanout.thread-id");
    return <Navigate to="/chat/$threadId" params={{ threadId: threadID }} replace />;
  }
  return <Navigate to="/chat" replace />;
}
