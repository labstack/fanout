import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { api, setApiToken, tryRefresh } from "@/api/client";

interface User {
  id: string;
  email: string;
  name?: string;
  role: string;
  active: boolean;
}

interface AuthCtx {
  user: User | null;
  isLoading: boolean;
  isAdmin: boolean;
  isOperator: boolean;
  login: (accessToken: string) => void;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthCtx>({
  user: null,
  isLoading: true,
  isAdmin: false,
  isOperator: false,
  login: () => {},
  logout: async () => {},
});

export function useAuth() {
  return useContext(AuthContext);
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [ready, setReady] = useState(false);

  // On mount, try to restore session from refresh token cookie
  useEffect(() => {
    (async () => {
      const refreshed = await tryRefresh();
      if (refreshed) {
        try {
          const me = await api<User>("/api/auth/me");
          setUser(me);
        } catch {
          setUser(null);
        }
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
    } catch {
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

  // Don't render children until initial refresh attempt completes
  if (!ready) {
    return null;
  }

  return (
    <AuthContext.Provider value={{ user, isLoading, isAdmin, isOperator, login, logout }}>
      {children}
    </AuthContext.Provider>
  );
}
