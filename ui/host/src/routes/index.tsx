import { createFileRoute } from "@tanstack/react-router";
import { ChatPage } from "../App";

export const Route = createFileRoute("/")({
  component: ChatPage,
});
