import { DataState } from "@labstack/ui";
import { useEffect, useMemo, useRef } from "react";
import { Box, MantineProvider } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { pitotTheme, pitotCssVariables } from "../../../theme";
import { FragmentView } from "../dashboards/fragment-view";
import { appTransport } from "./transport";
import { usePanelApp } from "./use-panel-app";
import { observeContentSize } from "./size-reporter";
const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
export function PanelApp() {
  const { app, fragment, host, error, toolError } = usePanelApp();
  const wrapper = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (app && wrapper.current) return observeContentSize(wrapper.current, size => app.sendSizeChanged(size));
  }, [app]);
  const transport = useMemo(() => app ? appTransport(app) : undefined, [app]);
  // Follow the host theme; without one, follow the system setting.
  const systemDark = useMediaQuery("(prefers-color-scheme: dark)", undefined, { getInitialValueInEffect: false });
  const dark = host?.theme ? host.theme === "dark" : systemDark;
  const failure = toolError ?? (error ? "This view could not be loaded. Please try again." : null);
  return <MantineProvider theme={pitotTheme} cssVariablesResolver={pitotCssVariables} forceColorScheme={dark ? "dark" : "light"}>
    <QueryClientProvider client={queryClient}><div ref={wrapper} role="main" aria-label="Pitot analysis view" data-app-content>
      {fragment && transport && host ? <>
        {failure && <Box m="md"><DataState state="error" error={{ message: failure }} /></Box>}
        <FragmentView fragment={fragment} dark={dark} onQuery={transport.query} drillClient={transport.drill} resolveVariables={transport.resolveVariables} hostDisplayMode={host.displayMode ?? "inline"}
          onDisplayMode={host.availableDisplayModes?.includes("fullscreen") ? async mode => (await app!.requestDisplayMode({ mode })).mode === mode : undefined} />
      </> : failure ? <Box m="md"><DataState state="error" error={{ message: failure }} /></Box> : <DataState state="loading" loadingLabel="Loading panel view" />}
    </div></QueryClientProvider>
  </MantineProvider>;
}
