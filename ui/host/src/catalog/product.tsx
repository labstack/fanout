import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { useMemo } from "react";
import { routeTree } from "../routeTree.gen";
import { parseSearch, stringifySearch } from "../search-encoding";

export const pageParameters = {
  layout: "fullscreen",
  docs: { story: { inline: false, iframeHeight: 800 } },
};

export function Product({ path = "/chat" }: { path?: string }) {
  const router = useMemo(() => createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
    parseSearch,
    stringifySearch,
    defaultPreload: false,
  }), [path]);
  return <RouterProvider router={router} />;
}
