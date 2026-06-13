import { lazy, Suspense } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router";
import { RootLayout } from "./components/layout/root-layout";
import { AuthProvider, RequireAuth } from "./hooks/use-auth";
import { LoadingState } from "./components/states/loading-state";

// Route-level code splitting: each page (and the heavy chart/d3 libs it pulls
// in) loads on demand, so the initial bundle stays small.
const HomePage = lazy(() =>
  import("./pages/home-page").then((m) => ({ default: m.HomePage })),
);
const ChatPage = lazy(() =>
  import("./pages/chat-page").then((m) => ({ default: m.ChatPage })),
);
const ServicePage = lazy(() =>
  import("./pages/service-page").then((m) => ({ default: m.ServicePage })),
);
const AlertsPage = lazy(() =>
  import("./pages/alerts-page").then((m) => ({ default: m.AlertsPage })),
);
const LoginPage = lazy(() =>
  import("./pages/login-page").then((m) => ({ default: m.LoginPage })),
);
const SettingsPage = lazy(() =>
  import("./pages/settings-page").then((m) => ({ default: m.SettingsPage })),
);
const DemoPage = lazy(() =>
  import("./pages/demo-page").then((m) => ({ default: m.DemoPage })),
);

function RouteFallback() {
  return (
    <div className="flex h-screen items-center justify-center bg-background">
      <LoadingState className="w-full max-w-2xl px-6" />
    </div>
  );
}

function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Suspense fallback={<RouteFallback />}>
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route
              element={
                <RequireAuth>
                  <RootLayout />
                </RequireAuth>
              }
            >
              <Route index element={<HomePage />} />
              <Route path="/service/:name" element={<ServicePage />} />
              <Route path="/alerts" element={<AlertsPage />} />
              <Route path="/chat" element={<ChatPage />} />
              <Route path="/settings" element={<SettingsPage />} />
              <Route path="/demo" element={<DemoPage />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Route>
          </Routes>
        </Suspense>
      </AuthProvider>
    </BrowserRouter>
  );
}

export default App;
