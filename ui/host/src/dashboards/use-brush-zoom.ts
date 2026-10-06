import { useCallback, useRef } from "react";
import { brushRange } from "../../../panels/interaction";
import type { DashboardSearch } from "./search";

export function useBrushZoom(search: DashboardSearch, onSearch: (next: DashboardSearch, replace?: boolean) => void) {
  const scope = JSON.stringify([search.range, search.from, search.to]);
  const current = useRef({ search, onSearch, scope });
  const last = useRef("");
  if (current.current.scope !== scope) last.current = "";
  current.current = { search, onSearch, scope };
  const reset = useCallback(() => { last.current = ""; }, []);
  const zoom = useCallback((from: number, to: number) => {
    const absolute = brushRange(from, to);
    if (!absolute) return;
    const active = current.current;
    const key = `${absolute.from}/${absolute.to}`;
    if (active.search.from === absolute.from && active.search.to === absolute.to || last.current === key) return;
    last.current = key;
    active.onSearch({ ...active.search, range: undefined, ...absolute }, false);
  }, []);
  return { zoom, reset };
}
