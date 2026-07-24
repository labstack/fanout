import { createFileRoute } from "@tanstack/react-router";
import { ChatPage } from "../App";

export const Route = createFileRoute("/chat/$threadId")({
  component: ChatPage,
});
