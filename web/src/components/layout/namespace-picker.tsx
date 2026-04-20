import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { clsx } from "clsx";
import { api } from "@/api/client";

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
      } catch (err) { console.warn("namespace fetch failed:", err); }
    }
    load();
    const interval = setInterval(load, 60_000);
    return () => { cancelled = true; clearInterval(interval); };
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
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          className={clsx(
            "flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-[11px] mono transition-colors cursor-pointer",
            current
              ? "border-primary/30 bg-primary/8 text-primary"
              : "border-border/60 bg-surface-1/70 text-muted-foreground hover:text-foreground",
          )}
        >
          {current && (
            <span className="w-1.5 h-1.5 rounded-full bg-primary" />
          )}
          {label}
          <span className="text-[9px] opacity-50">{"\u25BE"}</span>
        </button>
      </DropdownMenu.Trigger>

      <DropdownMenu.Portal>
        <DropdownMenu.Content align="end" sideOffset={4} className="dropdown-content">
          <div className="px-2.5 py-1.5 text-[9px] text-muted-foreground uppercase tracking-wider mono font-semibold">
            Namespace
          </div>
          {namespaces.map((ns) => (
            <DropdownMenu.Item
              key={ns}
              className="dropdown-item"
              onSelect={() => select(ns)}
            >
              <span className={clsx("w-1.5 h-1.5 rounded-full shrink-0", current === ns ? "bg-primary" : "bg-surface-3")} />
              <span className={clsx(current === ns && "text-foreground")}>{ns}</span>
            </DropdownMenu.Item>
          ))}
          <DropdownMenu.Separator className="my-1 h-px bg-border/30" />
          <DropdownMenu.Item
            className="dropdown-item"
            onSelect={() => select("")}
          >
            <span className={clsx("w-1.5 h-1.5 rounded-full shrink-0", !current ? "bg-primary" : "bg-surface-3")} />
            <span className={clsx(!current && "text-foreground")}>All namespaces</span>
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}
