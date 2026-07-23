import { createFileRoute, Navigate } from "@tanstack/react-router";
import { useMemo } from "react";
import { createID } from "../id";

export const Route = createFileRoute("/chat/")({
  component: NewChat,
});

function NewChat() {
  const threadID = useMemo(() => createID(), []);
  return <Navigate to="/chat/$threadId" params={{ threadId: threadID }} replace />;
}
