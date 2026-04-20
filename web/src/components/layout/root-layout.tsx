import { useRef, useEffect, Component, type ReactNode } from "react";
import { Outlet, useLocation } from "react-router";
import { Toaster } from "sonner";
import { NavLoader } from "./nav-loader";
import { Nav } from "./nav";
import { Footer } from "./footer";
import { setApiToken } from "@/api/client";

const HIDE_FOOTER = new Set(["/chat"]);

class ErrorBoundary extends Component<
  { children: ReactNode },
  { hasError: boolean; error?: Error }
> {
  state = { hasError: false, error: undefined as Error | undefined };

  static getDerivedStateFromError(error: Error) {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error("[App] Unhandled render error:", error, info);
  }

  render() {
    if (this.state.hasError) {
      return (
        <div className="flex items-center justify-center h-screen bg-surface text-foreground">
          <div className="text-center max-w-md px-6">
            <h1 className="font-heading text-lg font-semibold mb-2">
              Something went wrong
            </h1>
            <p className="text-sm text-muted-foreground mb-6">
              {this.state.error?.message}
            </p>
            <button
              onClick={() => window.location.reload()}
              className="btn-primary"
            >
              Reload
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}

export function RootLayout() {
  const { pathname, search } = useLocation();
  const mainRef = useRef<HTMLElement>(null);
  const showFooter = !HIDE_FOOTER.has(pathname);

  useEffect(() => {
    mainRef.current?.scrollTo(0, 0);
  }, [pathname]);

  useEffect(() => {
    const token = new URLSearchParams(search).get("token");
    if (token) {
      setApiToken(token);
    }
  }, [search]);

  return (
    <div className="h-screen flex flex-col noise">
      <NavLoader />
      <Nav />
      <main
        ref={mainRef}
        className="flex-1 min-h-0 overflow-y-auto flex flex-col"
      >
        <ErrorBoundary>
          <div className="flex-1">
            <Outlet />
          </div>
          {showFooter && <Footer />}
        </ErrorBoundary>
      </main>
      <Toaster
        theme="dark"
        position="bottom-right"
        toastOptions={{
          style: {
            background: "var(--surface-2)",
            border: "1px solid var(--border)",
            color: "#d4d4d8",
            fontSize: "0.8125rem",
          },
        }}
      />
    </div>
  );
}
