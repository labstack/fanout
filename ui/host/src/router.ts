import { createRouter } from "@tanstack/react-router";
import { routeTree } from "./routeTree.gen";

import { parseSearch, stringifySearch } from "./search-encoding";

export const router = createRouter({
  routeTree,
  parseSearch,
  stringifySearch,
  defaultPreload: "intent",
  scrollRestoration: true,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
