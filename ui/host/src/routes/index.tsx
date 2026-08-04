import { createFileRoute, Navigate } from "@tanstack/react-router";

export const Route = createFileRoute("/")({
  component: LegacyChatRedirect,
});

function LegacyChatRedirect() {
  const threadID = localStorage.getItem("fanout.thread-id");
  if (threadID) {
    localStorage.removeItem("fanout.thread-id");
    return <Navigate to="/chat/$threadId" params={{ threadId: threadID }} replace />;
  }
  return <Navigate to="/chat" replace />;
}
