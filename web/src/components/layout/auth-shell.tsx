import type * as React from "react";
import { cn } from "@/lib/utils";

interface AuthShellProps {
  children: React.ReactNode;
  className?: string;
}

export function AuthShell({ children, className }: AuthShellProps) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4 py-12 noise">
      <div className={cn("w-full max-w-sm", className)}>{children}</div>
    </div>
  );
}
