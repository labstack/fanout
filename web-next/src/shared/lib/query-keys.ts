// Namespace-aware key factory. The auth token is NEVER part of a key —
// auth changes invalidate queries instead (see providers).
export const keys = {
  overview: (window: number, namespace: string) => ["overview", window, namespace] as const,
};
