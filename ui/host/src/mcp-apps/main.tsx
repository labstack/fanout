import { StrictMode, useMemo } from "react";
import { createRoot } from "react-dom/client";
import { Alert, Center, Loader, MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fanoutTheme } from "../theme";
import { fanoutCssVariables } from "../../../theme";
import { FragmentView } from "../dashboards/fragment-view";
import { appTransport } from "./transport";
import { usePanelApp } from "./use-panel-app";
import "@mantine/core/styles.css";
import "@fontsource-variable/geist";
import "@fontsource-variable/geist-mono";
import "../index.css";
import "./app.css";

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
export function PanelApp() {
  const { app, fragment, host, error, toolError } = usePanelApp();
  const transport = useMemo(() => app ? appTransport(app) : undefined, [app]);
  const dark = host?.theme === "dark";
  const failure = toolError ?? (error ? "This view could not be loaded. Please try again." : null);
  return <MantineProvider theme={fanoutTheme} cssVariablesResolver={fanoutCssVariables} forceColorScheme={dark ? "dark" : "light"}>
    <QueryClientProvider client={queryClient}>
      {failure ? <Alert color="bad" m="md">{failure}</Alert> : !app || !host || !fragment || !transport ? <Center mih={180}><Loader size="sm" aria-label="Loading panel view" /></Center> :
        <FragmentView fragment={fragment} dark={dark} onQuery={transport.query} drillClient={transport.drill} resolveVariables={transport.resolveVariables} />}
    </QueryClientProvider>
  </MantineProvider>;
}
createRoot(document.getElementById("root")!).render(<StrictMode><PanelApp /></StrictMode>);
