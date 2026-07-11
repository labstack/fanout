import { QueryClient } from "@tanstack/react-query";

export const REFRESH_INTERVAL = 30_000;

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      refetchInterval: REFRESH_INTERVAL,
      retry: 1,
    },
  },
});
