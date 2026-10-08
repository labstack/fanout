import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { PanelApp } from "./panel-app";
import "@mantine/core/styles.css";
import "@fontsource-variable/geist";
import "@fontsource-variable/geist-mono";
import "../dashboards/dashboard.css";
import "./app.css";

createRoot(document.getElementById("root")!).render(<StrictMode><PanelApp /></StrictMode>);
