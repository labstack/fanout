import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { Navigate, useLocation } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, setApiToken, tryRefresh } from "@/api/client";
import { AuthContext, useAuth, type AuthStatus, type User } from "./auth-context";

export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const [user, setUser] = useState<User | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [setupRequired, setSetupRequired] = useState(false);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    (async () => {
      try {
        const status = await fetch("/api/auth/status").then((r) => r.json()) as AuthStatus;
        setSetupRequired(status.setup_required);

        if (!status.setup_required) {
          const refreshed = await tryRefresh();
          // On a public-read demo, an anonymous visitor still gets a read-only
          // viewer from /api/auth/me (synthesized server-side), so fetch the
          // user even without a refresh cookie — that's what lets the demo
          // render its dashboards without a login.
          if (refreshed || status.public_read) {
            try {
              const me = await api<User>("/api/auth/me");
              setUser(me);
            } catch (err) {
              console.error("auth: failed to fetch user", err);
              setUser(null);
            }
          }
        }
      } catch {
        // Auth endpoints don't exist (very old build) — skip
      }

      setIsLoading(false);
      setReady(true);
    })();
  }, []);

  const login = useCallback(async (accessToken: string) => {
    setApiToken(accessToken);
    try {
      const me = await api<User>("/api/auth/me");
      setUser(me);
      setSetupRequired(false);
    } catch (err) {
      console.error("auth: login fetch user failed", err);
      setUser(null);
    }
  }, []);

  const logout = useCallback(async () => {
    try {
      await fetch("/api/auth/logout", {
        method: "POST",
        credentials: "include",
      });
    } catch { /* ignore */ }
    setApiToken(null);
    setUser(null);
    // Drop all cached query data so a subsequent login doesn't surface the
    // previous user's overview/alerts/etc. (queryKeys don't include the token).
    queryClient.clear();
  }, [queryClient]);

  const isAdmin = user?.role === "admin";
  const isOperator = isAdmin || user?.role === "operator";

  const value = useMemo(
    () => ({ user, isLoading, isAdmin, isOperator, setupRequired, login, logout }),
    [user, isLoading, isAdmin, isOperator, setupRequired, login, logout],
  );

  if (!ready) {
    return null;
  }

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

/** Wraps protected routes — redirects to /login if not authenticated or setup needed. */
export function RequireAuth({ children }: { children: ReactNode }) {
  const { user, isLoading, setupRequired } = useAuth();
  const location = useLocation();

  if (isLoading) return null;

  if (setupRequired || !user) {
    return <Navigate to={`/login?next=${encodeURIComponent(location.pathname)}`} replace />;
  }

  return <>{children}</>;
}
