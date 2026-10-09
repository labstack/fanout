import { useEffect, useState } from "react";

/** Browser-local, viewer-separated dashboard preference; storage may be unavailable. */
export function useShortcutPreference(viewer: string) {
  const key = "fanout:single-key-shortcuts:" + viewer;
  const read = () => { try { return localStorage.getItem(key) !== "off"; } catch { return true; } };
  const [enabled, setEnabled] = useState(read);
  useEffect(() => { setEnabled(read()); }, [key]);
  useEffect(() => {
    const change = (event: StorageEvent) => { if (event.key === key) setEnabled(read()); };
    window.addEventListener("storage", change);
    return () => window.removeEventListener("storage", change);
  }, [key]);
  const change = (next: boolean) => { setEnabled(next); try { localStorage.setItem(key, next ? "on" : "off"); } catch { /* Keep the setting in memory when browser storage is unavailable. */ } };
  return {enabled, change};
}
