import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "@tanstack/react-router";
import { MantineProvider } from "@mantine/core";
import { router } from "./router";
import { fanoutTheme } from "./theme";
import { fanoutCssVariables } from "../../theme";
import "@mantine/core/styles.css";
// The typeface is shipped rather than named. The previous stack asked for Inter
// and never loaded it, so the product rendered in whatever sans the machine
// happened to have.
import "@fontsource/ibm-plex-sans/latin-400.css";
import "@fontsource/ibm-plex-sans/latin-400-italic.css";
import "@fontsource/ibm-plex-sans/latin-500.css";
import "@fontsource/ibm-plex-sans/latin-600.css";
import "@fontsource/ibm-plex-sans/latin-700.css";
import "@fontsource/ibm-plex-mono/latin-400.css";
import "@fontsource/ibm-plex-mono/latin-500.css";
import "@fontsource/ibm-plex-mono/latin-600.css";
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
