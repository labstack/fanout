import type * as React from "react";
import { Toaster as SonnerToaster } from "sonner";

export function Toaster(props: React.ComponentProps<typeof SonnerToaster>) {
  return (
    <SonnerToaster
      theme="dark"
      position="bottom-right"
      toastOptions={{
        style: {
          background: "var(--surface-2)",
          border: "1px solid var(--border)",
          color: "var(--foreground)",
          fontSize: "0.8125rem",
        },
      }}
      {...props}
    />
  );
}
