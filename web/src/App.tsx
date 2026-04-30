import { BrowserRouter, Navigate, Route, Routes } from "react-router";
import { RootLayout } from "./components/layout/root-layout";
import { AuthProvider, RequireAuth } from "./hooks/use-auth";
import { HomePage } from "./pages/home-page";
import { ChatPage } from "./pages/chat-page";
import { ServicePage } from "./pages/service-page";
import { AlertsPage } from "./pages/alerts-page";
import { LoginPage } from "./pages/login-page";
import { SettingsPage } from "./pages/settings-page";
import { DemoPage } from "./pages/demo-page";

function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
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
      </AuthProvider>
    </BrowserRouter>
  );
}

export default App;
