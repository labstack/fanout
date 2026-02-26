import { BrowserRouter, Routes, Route } from "react-router";
import { ChatPage } from "./pages/ChatPage";

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/*" element={<ChatPage />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
