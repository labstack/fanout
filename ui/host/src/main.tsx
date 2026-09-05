import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "@tanstack/react-router";
import { MantineProvider } from "@mantine/core";
import { router } from "./router";
import { fanoutTheme } from "./theme";
import { fanoutCssVariables } from "../../theme";
import "@mantine/core/styles.css";
// The typeface is shipped rather than named: one variable file per family
// covers every weight the app uses.
import "@fontsource-variable/geist";
import "@fontsource-variable/geist-mono";
import "react-grid-layout/css/styles.css";
import "react-resizable/css/styles.css";
import "./index.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <MantineProvider theme={fanoutTheme} defaultColorScheme="auto" cssVariablesResolver={fanoutCssVariables}>
      <RouterProvider router={router} />
    </MantineProvider>
  </StrictMode>,
);
