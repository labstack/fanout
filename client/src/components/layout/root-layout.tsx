import { useRef, useEffect } from "react";
import { Outlet, useLocation } from "react-router";
import { Toaster } from "sonner";
import { NavLoader } from "./nav-loader";
import { Nav } from "./nav";
import { Footer } from "./footer";

const HIDE_FOOTER = new Set(["/"]);

export function RootLayout() {
  const { pathname } = useLocation();
  const mainRef = useRef<HTMLElement>(null);
  const showFooter = !HIDE_FOOTER.has(pathname);

  useEffect(() => {
    mainRef.current?.scrollTo(0, 0);
  }, [pathname]);

  return (
    <div className="h-screen flex flex-col noise">
      <NavLoader />
      <Nav />
      <main
        ref={mainRef}
        className="flex-1 min-h-0 overflow-y-auto flex flex-col"
      >
        <div className="flex-1">
          <Outlet />
        </div>
        {showFooter && <Footer />}
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
