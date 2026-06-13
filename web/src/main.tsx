import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import {
  QueryClient,
  QueryClientProvider,
  QueryCache,
} from "@tanstack/react-query";
import { toast } from "sonner";
import { isApiError } from "./api/client";
import "./index.css";
import App from "./App";

const message = (error: Error) =>
  isApiError(error) ? error.message : "An unexpected error occurred";

const queryClient = new QueryClient({
  // Only surface a toast when a query fails with nothing cached to show
  // (i.e. the initial load). Background refetch failures keep the last good
  // data on screen and are conveyed by each page's freshness indicator, so
  // toasting them every poll interval would be noise. Mutations are
  // user-initiated and toast their own (more specific) errors locally.
  queryCache: new QueryCache({
    onError: (error, query) => {
      if (query.state.data === undefined) toast.error(message(error));
    },
  }),
  defaultOptions: {
    queries: {
      staleTime: 60_000,
      retry: (count, error) => {
        if (isApiError(error) && error.status < 500) return false;
        return count < 1;
      },
    },
  },
});

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("#root element missing");
createRoot(rootEl).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
