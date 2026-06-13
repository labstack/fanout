import { useEffect, useState } from "react";

/**
 * Seconds elapsed since `updatedAt` (a `Date.now()`-style epoch ms, e.g.
 * TanStack Query's `dataUpdatedAt`). Recomputed on a fixed cadence so the
 * "Live / Ns ago" freshness label stays current without coupling to the
 * fetch/poll loop. Resets to ~0 whenever `updatedAt` changes (fresh data).
 */
export function useFreshness(updatedAt: number, tickMs = 5_000): number {
  const [seconds, setSeconds] = useState(0);

  useEffect(() => {
    if (!updatedAt) return; // no data yet — stays at the initial 0 ("Live")
    // Date.now() lives in the effect/interval (not render) to stay pure.
    const update = () => setSeconds(Math.floor((Date.now() - updatedAt) / 1000));
    // Seed immediately so the label resets the moment fresh data arrives.
    // eslint-disable-next-line react-hooks/set-state-in-effect -- timer-driven clock
    update();
    const id = setInterval(update, tickMs);
    return () => clearInterval(id);
  }, [updatedAt, tickMs]);

  return seconds;
}
