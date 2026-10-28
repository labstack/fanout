import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useEffect, useMemo, type ReactNode } from "react";

export function QueryFrame({ children }: { children: ReactNode }) {
  const client = useMemo(() => new QueryClient({ defaultOptions: { queries: { retry: false } } }), []);
  useEffect(() => () => client.clear(), [client]);
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
