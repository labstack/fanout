import { layoutServiceMapRaw, type CardWidths } from "./service-map-layout";
import type { ServiceGraph } from "../../../../panels/rollups";
import type { ChartSize } from "../../../../panels/compile";
self.onmessage = (event: MessageEvent<{model:ServiceGraph;size:ChartSize;widths:CardWidths}>) => {
  const {model,size,widths} = event.data;
  self.postMessage(layoutServiceMapRaw(model,size,widths));
};
