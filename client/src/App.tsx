import { BrowserRouter, Routes, Route } from "react-router";
import { RootLayout } from "./components/layout/root-layout";
import { ChatPage } from "./pages/ChatPage";
import { DemoPage } from "./pages/DemoPage";

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<RootLayout />}>
          <Route path="/demo" element={<DemoPage />} />
          <Route path="/*" element={<ChatPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

export default App;
