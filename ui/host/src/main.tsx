import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "@tanstack/react-router";
import { MantineProvider } from "@mantine/core";
import { router } from "./router";
import { pitotTheme, pitotCssVariables } from "../../theme";
import "@mantine/core/styles.css";
import "@labstack/ui/styles.css";
// The typeface is shipped rather than named: one variable file per family
// covers every weight the app uses.
import "@fontsource-variable/geist";
import "@fontsource-variable/geist-mono";
import "react-grid-layout/css/styles.css";
import "react-resizable/css/styles.css";
import "./index.css";
import "./dashboards/dashboard.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <MantineProvider theme={pitotTheme} defaultColorScheme="auto" cssVariablesResolver={pitotCssVariables}>
      <RouterProvider router={router} />
    </MantineProvider>
  </StrictMode>,
);
