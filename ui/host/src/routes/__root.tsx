import { createRootRoute, Navigate } from "@tanstack/react-router";
import App from "../app";

export const Route = createRootRoute({
  component: App,
  notFoundComponent: () => <Navigate to="/" replace />,
});
