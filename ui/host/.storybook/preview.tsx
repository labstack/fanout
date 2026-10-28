import "@mantine/core/styles.css";
import "@fontsource-variable/geist";
import "@fontsource-variable/geist-mono";
import "react-grid-layout/css/styles.css";
import "react-resizable/css/styles.css";
import "../src/index.css";
import "../src/dashboards/dashboard.css";
import { MantineProvider } from "@mantine/core";
import { type Args, INTERNAL_DEFAULT_PROJECT_ANNOTATIONS, type Preview, type StoryContext } from "@storybook/react-vite";
import { UPDATE_STORY_ARGS } from "storybook/internal/core-events";
import { addons } from "storybook/preview-api";
import { configure, expect, mocked, sb, spyOn } from "storybook/test";
import { fanoutTheme } from "../src/theme";
import { fanoutCssVariables } from "../../theme";
import { makeService, type CatalogService, type Scenario } from "../src/catalog/requests";
import { createID } from "../src/id";
import { storyNow } from "../src/catalog/fixtures";
import { assertTextLayout } from "./checks";
import { assertScrollbarsClear } from "./scrollbars";
import { viewportOptions } from "./viewports";

sb.mock("../src/id.ts", { spy: true });

configure({ asyncUtilTimeout: 10_000 });
const renderedStories = new WeakSet<StoryContext>();
const services = new WeakMap<StoryContext, CatalogService>();
const preview: Preview = {
  tags: ["autodocs"],
  parameters: {
    layout: "padded",
    a11y: { test: "error" },
    viewport: { options: viewportOptions },
    options: { storySort: { order: ["Pages", "Flows", "Components", "Layout", "Embedded"] } },
  },
  globalTypes: {
    colorScheme: { description: "Color scheme", toolbar: { title: "Color scheme", items: ["light", "dark"], dynamicTitle: true } },
    pace: { description: "Mock stream pacing", toolbar: { title: "Stream pace", items: [{ value: "fast", title: "Fast" }, { value: "recording", title: "Recording" }], dynamicTitle: true } },
  },
  initialGlobals: { colorScheme: "light", pace: "fast", viewport: { value: "desktop", isRotated: false } },
  renderToCanvas: async (renderContext, canvas) => {
    const render = INTERNAL_DEFAULT_PROJECT_ANNOTATIONS.renderToCanvas;
    if (!render) throw new Error("Storybook React renderer is unavailable");
    const context = renderContext.storyContext;
    const unmount = await render({ ...renderContext, forceRemount: context.parameters.__isPortableStory ? !renderedStories.has(context) : renderContext.forceRemount }, canvas);
    renderedStories.add(context);
    return () => { renderedStories.delete(context); return unmount?.(); };
  },
  beforeEach: context => {
    const RealDate = Date;
    globalThis.Date = new Proxy(RealDate, {
      construct: (target, args) => Reflect.construct(target, args.length ? args : [storyNow]),
      apply: () => new RealDate(storyNow).toString(),
      get: (target, key, receiver) => key === "now" ? () => storyNow : Reflect.get(target, key, receiver),
    });
    const store = new Map<string, string>();
    spyOn(Storage.prototype, "getItem").mockImplementation(key => store.get(key) ?? null);
    spyOn(Storage.prototype, "setItem").mockImplementation((key, value) => { store.set(key, String(value)); });
    spyOn(Storage.prototype, "removeItem").mockImplementation(key => { store.delete(key); });
    spyOn(Storage.prototype, "clear").mockImplementation(() => store.clear());
    spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
    const scenario = context.parameters.scenario as Scenario | undefined;
    const service = makeService({ ...scenario, pace: scenario?.pace ?? (context.globals.pace === "recording" ? "recording" : "fast") }, window.fetch.bind(window));
    services.set(context, service);
    spyOn(window, "fetch").mockImplementation(service.handle);
    let id = 0;
    mocked(createID).mockImplementation(() => `catalog-${String(++id).padStart(4, "0")}`);
    const channel = addons.getChannel();
    const onUpdate = async ({ storyId, updatedArgs }: { storyId: string; updatedArgs: Args }) => {
      if (storyId !== context.id || !context.renderToCanvas) return;
      Object.assign(context.args, updatedArgs);
      await context.renderToCanvas();
    };
    if (context.parameters.__isPortableStory) channel.on(UPDATE_STORY_ARGS, onUpdate);
    return () => { service.dispose(); services.delete(context); globalThis.Date = RealDate; channel.off(UPDATE_STORY_ARGS, onUpdate); };
  },
  afterEach: async context => {
    await document.fonts.ready;
    expect(services.get(context)?.unexpected ?? [], "Unexpected backend requests").toEqual([]);
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    await Promise.all(document.getAnimations().filter(animation => animation.effect?.getTiming().iterations !== Infinity).map(animation => animation.finished.catch(() => undefined)));
    assertTextLayout(document.body);
    assertScrollbarsClear(document.body);
  },
  decorators: [(Story, { globals, parameters }) => <MantineProvider key={globals.colorScheme} theme={fanoutTheme} cssVariablesResolver={fanoutCssVariables} defaultColorScheme={globals.colorScheme === "dark" ? "dark" : "light"} forceColorScheme={parameters.interactiveColorScheme ? undefined : globals.colorScheme === "dark" ? "dark" : "light"}><Story /></MantineProvider>],
};
export default preview;
