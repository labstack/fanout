import { DataState, Notice } from "@labstack/ui";
import { useEffect, useMemo, useRef } from "react";
import { Box,  Center, MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fanoutTheme, fanoutCssVariables } from "../../../theme";
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
  const dark = host?.theme !== "light";
  const failure = toolError ?? (error ? "This view could not be loaded. Please try again." : null);
  return <MantineProvider theme={fanoutTheme} cssVariablesResolver={fanoutCssVariables} forceColorScheme={dark ? "dark" : "light"}>
    <QueryClientProvider client={queryClient}><div ref={wrapper} role="main" aria-label="Fanout analysis view" data-app-content>
      {fragment && transport && host ? <>
        {failure && <Box m="md"><Notice tone="danger">{failure}</Notice></Box>}
        <FragmentView fragment={fragment} dark={dark} onQuery={transport.query} drillClient={transport.drill} resolveVariables={transport.resolveVariables} hostDisplayMode={host.displayMode ?? "inline"}
          onDisplayMode={host.availableDisplayModes?.includes("fullscreen") ? async mode => (await app!.requestDisplayMode({ mode })).mode === mode : undefined} />
      </> : failure ? <Box m="md"><Notice tone="danger">{failure}</Notice></Box> : <Center mih={180}><Box role="status" aria-label="Loading panel view"><DataState state="loading" loadingLabel="Loading panel view" rows={3} /></Box></Center>}
    </div></QueryClientProvider>
  </MantineProvider>;
}
