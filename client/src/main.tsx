import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { toast } from "sonner";
import { isApiError } from "./api/client";
import "./index.css";
import App from "./App";

const queryClient = new QueryClient({
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

queryClient.getQueryCache().config.onError = (error) => {
  toast.error(isApiError(error) ? error.message : "An unexpected error occurred");
};

queryClient.getMutationCache().config.onError = (error) => {
  toast.error(isApiError(error) ? error.message : "An unexpected error occurred");
};

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
