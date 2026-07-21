import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { AppearanceProvider } from "./appearance";
import "./index.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AppearanceProvider><App /></AppearanceProvider>
  </StrictMode>,
);
