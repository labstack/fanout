import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router";
import { api, setApiToken, tryRefresh } from "@/api/client";

interface User {
  id: string;
  email: string;
  name?: string;
  role: string;
  active: boolean;
}

interface AuthStatus {
  setup_required: boolean;
  auth_enabled: boolean;
}

interface AuthCtx {
  user: User | null;
  isLoading: boolean;
  isAdmin: boolean;
  isOperator: boolean;
  setupRequired: boolean;
  login: (accessToken: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthCtx>({
  user: null,
  isLoading: true,
  isAdmin: false,
  isOperator: false,
  setupRequired: false,
  login: async () => {},
  logout: async () => {},
});

export function useAuth() {
  return useContext(AuthContext);
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [setupRequired, setSetupRequired] = useState(false);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    (async () => {
      // Check auth status first
      try {
        const status = await fetch("/api/auth/status").then((r) => r.json()) as AuthStatus;
        setSetupRequired(status.setup_required);

        if (!status.setup_required) {
          // Try to restore session
          const refreshed = await tryRefresh();
          if (refreshed) {
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

  async function login(accessToken: string) {
    setApiToken(accessToken);
    try {
      const me = await api<User>("/api/auth/me");
      setUser(me);
      setSetupRequired(false);
    } catch (err) {
      console.error("auth: login fetch user failed", err);
      setUser(null);
    }
  }

  async function logout() {
    try {
      await fetch("/api/auth/logout", {
        method: "POST",
        credentials: "include",
      });
    } catch { /* ignore */ }
    setApiToken(null);
    setUser(null);
  }

  const isAdmin = user?.role === "admin";
  const isOperator = isAdmin || user?.role === "operator";

  if (!ready) {
    return null;
  }

  return (
    <AuthContext.Provider value={{ user, isLoading, isAdmin, isOperator, setupRequired, login, logout }}>
      {children}
    </AuthContext.Provider>
  );
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
