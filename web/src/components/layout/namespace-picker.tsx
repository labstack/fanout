import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { api } from "@/api/client";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

export function NamespacePicker() {
  const { pathname, search } = useLocation();
  const navigate = useNavigate();
  const params = new URLSearchParams(search);
  const current = params.get("namespace") || "";

  const [namespaces, setNamespaces] = useState<string[]>([]);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const ns = await api<string[]>("/api/namespaces");
        if (!cancelled) setNamespaces(ns ?? []);
      } catch (err) {
        console.warn("namespace fetch failed:", err);
      }
    }
    load();
    const interval = setInterval(load, 60_000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, []);

  if (namespaces.length <= 1) return null;

  function select(ns: string) {
    const newParams = new URLSearchParams(search);
    if (ns) {
      newParams.set("namespace", ns);
    } else {
      newParams.delete("namespace");
    }
    const query = newParams.toString();
    navigate(`${pathname}${query ? `?${query}` : ""}`, { replace: true });
  }

  const label = current || "All";

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className={cn(
            "flex items-center gap-1.5 rounded-md border px-2.5 py-1 font-mono text-[11px] transition-colors",
            current
              ? "border-primary/30 bg-primary/10 text-primary"
              : "border-border/60 bg-surface-1/70 text-muted-foreground hover:text-foreground",
          )}
        >
          {current && (
            <span className="size-1.5 rounded-full bg-primary" aria-hidden="true" />
          )}
          {label}
          <span className="text-[9px] opacity-50" aria-hidden="true">
            {"▾"}
          </span>
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" sideOffset={4}>
        <DropdownMenuLabel className="font-mono text-[9px] uppercase tracking-wider text-muted-foreground">
          Namespace
        </DropdownMenuLabel>
        {namespaces.map((ns) => (
          <DropdownMenuItem
            key={ns}
            onSelect={() => select(ns)}
            className="font-mono text-[13px]"
          >
            <span
              aria-hidden="true"
              className={cn(
                "size-1.5 shrink-0 rounded-full",
                current === ns ? "bg-primary" : "bg-surface-3",
              )}
            />
            <span className={cn(current === ns && "text-foreground")}>{ns}</span>
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={() => select("")}
          className="font-mono text-[13px]"
        >
          <span
            aria-hidden="true"
            className={cn(
              "size-1.5 shrink-0 rounded-full",
              !current ? "bg-primary" : "bg-surface-3",
            )}
          />
          <span className={cn(!current && "text-foreground")}>All namespaces</span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
