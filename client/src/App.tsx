import { BrowserRouter, Navigate, Route, Routes } from "react-router";
import { RootLayout } from "./components/layout/root-layout";
import { HomePage } from "./pages/HomePage";
import { ChatPage } from "./pages/ChatPage";
import { ServicePage } from "./pages/ServicePage";
import { AlertsPage } from "./pages/AlertsPage";
import { DemoPage } from "./pages/DemoPage";

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<RootLayout />}>
          <Route index element={<HomePage />} />
          <Route path="/service/:name" element={<ServicePage />} />
          <Route path="/alerts" element={<AlertsPage />} />
          <Route path="/chat" element={<ChatPage />} />
          <Route path="/demo" element={<DemoPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

export default App;
