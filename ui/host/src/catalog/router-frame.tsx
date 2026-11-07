import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { useMemo, type ReactNode } from "react";

export function RouterFrame({ children }: { children: ReactNode }) {
  const router = useMemo(() => createRouter({ routeTree: createRootRoute({ component: () => children }), history: createMemoryHistory() }), [children]);
  return <RouterProvider router={router} />;
}
