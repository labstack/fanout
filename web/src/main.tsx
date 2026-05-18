import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import {
  QueryClient,
  QueryClientProvider,
  QueryCache,
  MutationCache,
} from "@tanstack/react-query";
import { toast } from "sonner";
import { isApiError } from "./api/client";
import "./index.css";
import App from "./App";

const onError = (error: Error) => {
  toast.error(isApiError(error) ? error.message : "An unexpected error occurred");
};

const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({ onError }),
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
