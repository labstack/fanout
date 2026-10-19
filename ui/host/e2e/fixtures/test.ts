import { mergeTests } from "@playwright/test";
import { test as app } from "./app";
import { test as faults } from "./faults";
import { test as mcp } from "./mcp-host";
import { test as a11y } from "./a11y";
import { test as keyboard } from "./keyboard";

export const test = mergeTests(app, faults, mcp, a11y, keyboard);
export { expect } from "@playwright/test";
export type { APIRequestContext, Browser, Frame, FrameLocator, Locator, Page, Request, Response, TestInfo } from "@playwright/test";
