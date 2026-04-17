import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { api } from "@/api/client";

export function NamespacePicker() {
  const { pathname, search } = useLocation();
  const navigate = useNavigate();
  const params = new URLSearchParams(search);
  const current = params.get("namespace") || "";

  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  // Fetch discovered namespaces
  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const ns = await api<string[]>("/api/namespaces");
        if (!cancelled) setNamespaces(ns ?? []);
      } catch { /* ignore */ }
    }
    load();
    const interval = setInterval(load, 60_000);
    return () => { cancelled = true; clearInterval(interval); };
  }, []);

  // Close on outside click
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    if (open) document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, [open]);

  // Hide if only one or zero namespaces
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
    setOpen(false);
  }

  const label = current || "All";

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className={`flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-[11px] mono transition-colors cursor-pointer ${
          current
            ? "border-primary/30 bg-primary/8 text-primary"
            : "border-border/60 bg-surface-1/70 text-muted-foreground hover:text-foreground"
        }`}
      >
        {current && (
          <span className="w-1.5 h-1.5 rounded-full bg-primary" />
        )}
        {label}
        <span className="text-[9px] opacity-50">{open ? "\u25B4" : "\u25BE"}</span>
      </button>

      {open && (
        <div className="dropdown-content absolute top-8 right-0 min-w-[160px]">
          <div className="px-2.5 py-1.5 text-[9px] text-muted-foreground uppercase tracking-wider mono font-semibold">
            Namespace
          </div>
          {namespaces.map((ns) => (
            <button
              key={ns}
              type="button"
              onClick={() => select(ns)}
              className={`dropdown-item w-full ${current === ns ? "bg-surface-2 text-foreground" : ""}`}
            >
              <span className={`w-1.5 h-1.5 rounded-full ${current === ns ? "bg-primary" : "bg-surface-3"}`} />
              <span className="flex-1 text-left">{ns}</span>
              {current === ns && (
                <span className="text-primary text-[10px]">{"\u2713"}</span>
              )}
            </button>
          ))}
          <div className="border-t border-border/30 my-1" />
          <button
            type="button"
            onClick={() => select("")}
            className={`dropdown-item w-full ${!current ? "bg-surface-2 text-foreground" : ""}`}
          >
            <span className={`w-1.5 h-1.5 rounded-full border ${!current ? "border-primary bg-primary" : "border-surface-3"}`} />
            <span className="flex-1 text-left">All namespaces</span>
            {!current && (
              <span className="text-primary text-[10px]">{"\u2713"}</span>
            )}
          </button>
        </div>
      )}
    </div>
  );
}
