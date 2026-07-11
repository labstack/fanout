import { createContext, useCallback, useContext, useEffect, useState } from "react";

type Theme = "light" | "dark";
const ThemeCtx = createContext<{ theme: Theme | null; toggle: () => void }>({
  theme: null,
  toggle: () => {},
});

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setTheme] = useState<Theme | null>(null);
  useEffect(() => {
    if (theme) document.documentElement.setAttribute("data-theme", theme);
    else document.documentElement.removeAttribute("data-theme");
  }, [theme]);
  const toggle = useCallback(() => {
    setTheme((t) => {
      const current = t ?? (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
      return current === "dark" ? "light" : "dark";
    });
  }, []);
  return <ThemeCtx.Provider value={{ theme, toggle }}>{children}</ThemeCtx.Provider>;
}

export function useTheme() {
  return useContext(ThemeCtx);
}
