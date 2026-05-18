import { useRef, useEffect, Component, type ReactNode } from "react";
import { Outlet, useLocation } from "react-router";
import { NavLoader } from "./nav-loader";
import { Nav } from "./nav";
import { Footer } from "./footer";
import { Toaster } from "@/components/ui/sonner";
import { ErrorState } from "@/components/states/error-state";
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
        <div className="flex h-screen items-center justify-center bg-background">
          <div className="w-full max-w-md px-6">
            <ErrorState
              error={this.state.error}
              resetErrorBoundary={() => window.location.reload()}
            />
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
    <div className="flex h-screen flex-col noise">
      <NavLoader />
      <Nav />
      <main
        ref={mainRef}
        className="flex min-h-0 flex-1 flex-col overflow-y-auto"
      >
        <ErrorBoundary>
          <div className="flex-1">
            <Outlet />
          </div>
          {showFooter && <Footer />}
        </ErrorBoundary>
      </main>
      <Toaster />
    </div>
  );
}
