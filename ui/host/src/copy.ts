import { useEffect, useRef, useState } from "react";

export type CopyState = "idle" | "copied" | "failed";

/** Copy text to the clipboard and report the outcome for a moment.
 *
 * A denied permission refuses the write, and an insecure context has no
 * clipboard to refuse with; either way the caller shows the failure rather
 * than dropping a rejection nobody handles. The reset timer is held so a
 * second click is not cut short by the first click's timer, and it is
 * cleared when the component goes away. */
export function useCopy(resetAfter = 1500): { state: CopyState; copy: (text: string) => void } {
  const [state, setState] = useState<CopyState>("idle");
  const resetRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => { if (resetRef.current) clearTimeout(resetRef.current); }, []);
  function copy(text: string) {
    if (resetRef.current) clearTimeout(resetRef.current);
    setState("idle");
    const written = navigator.clipboard ? navigator.clipboard.writeText(text) : Promise.reject(new Error("clipboard unavailable"));
    void written.then(() => setState("copied"), () => setState("failed")).finally(() => { resetRef.current = setTimeout(() => setState("idle"), resetAfter); });
  }
  return { state, copy };
}
