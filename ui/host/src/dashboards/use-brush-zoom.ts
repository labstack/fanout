import { useCallback, useRef, useState } from "react";
import { brushRange } from "../../../panels/interaction";
import type { DashboardTime } from "../../../panels/types";
type BrushTime = Pick<DashboardTime, "range" | "from" | "to">;

export function useBrushZoom<T extends BrushTime>(search: T, onSearch: (next: T, replace?: boolean) => void) {
  const scope = JSON.stringify([search.range, search.from, search.to]);
  const current = useRef({ search, onSearch, scope });
  const last = useRef("");
  const restore = useRef<BrushTime | undefined>(undefined);
  const [zoomed, setZoomed] = useState(false);
  if (current.current.scope !== scope) {
    if (last.current !== `${search.from}/${search.to}`) {
      restore.current = undefined;
      if (zoomed) setZoomed(false);
    }
    last.current = "";
  }
  current.current = { search, onSearch, scope };
  const reset = useCallback(() => { last.current = ""; restore.current = undefined; setZoomed(false); }, []);
  const resetZoom = useCallback(() => {
    if (!restore.current) return;
    const previous = restore.current;
    reset();
    current.current.onSearch({ ...current.current.search, ...previous }, false);
  }, [reset]);
  const zoom = useCallback((from: number, to: number) => {
    const absolute = brushRange(from, to);
    if (!absolute) return;
    const active = current.current;
    const key = `${absolute.from}/${absolute.to}`;
    if (active.search.from === absolute.from && active.search.to === absolute.to || last.current === key) return;
    restore.current ??= { range: active.search.range, from: active.search.from, to: active.search.to };
    setZoomed(true);
    last.current = key;
    active.onSearch({ ...active.search, range: undefined, ...absolute }, false);
  }, []);
  return { zoom, reset, resetZoom, zoomed };
}
