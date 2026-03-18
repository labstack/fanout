import { BrowserRouter, Routes, Route } from "react-router";
import { ChatPage } from "./pages/ChatPage";
import { DemoPage } from "./pages/DemoPage";

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/demo" element={<DemoPage />} />
        <Route path="/*" element={<ChatPage />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
