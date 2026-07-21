import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "@tanstack/react-router";
import { MantineProvider } from "@mantine/core";
import { router } from "./router";
import { fanoutTheme } from "./theme";
import "@mantine/core/styles.css";
import "react-grid-layout/css/styles.css";
import "react-resizable/css/styles.css";
import "./index.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <MantineProvider theme={fanoutTheme} defaultColorScheme="light">
      <RouterProvider router={router} />
    </MantineProvider>
  </StrictMode>,
);
