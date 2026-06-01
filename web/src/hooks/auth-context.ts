import { createContext, useContext } from "react";

export interface User {
  id: string;
  email: string;
  name?: string;
  role: string;
  active: boolean;
  has_api_key?: boolean;
}

export interface AuthStatus {
  setup_required: boolean;
  auth_enabled: boolean;
}

export interface AuthCtx {
  user: User | null;
  isLoading: boolean;
  isAdmin: boolean;
  isOperator: boolean;
  setupRequired: boolean;
  login: (accessToken: string) => Promise<void>;
  logout: () => Promise<void>;
}

export const AuthContext = createContext<AuthCtx>({
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
