import { BrowserRouter, Navigate, Route, Routes } from "react-router";
import { RootLayout } from "./components/layout/root-layout";
import { AuthProvider, RequireAuth } from "./hooks/use-auth";
import { HomePage } from "./pages/HomePage";
import { ChatPage } from "./pages/ChatPage";
import { ServicePage } from "./pages/ServicePage";
import { AlertsPage } from "./pages/AlertsPage";
import { LoginPage } from "./pages/LoginPage";
import { DemoPage } from "./pages/DemoPage";

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
            <Route path="/demo" element={<DemoPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  );
}

export default App;
