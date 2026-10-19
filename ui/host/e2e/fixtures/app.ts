import { test as base } from "@playwright/test";
import { readFile } from "node:fs/promises";

export const test = base.extend<{}, { appEnvironment: void }>({
  appEnvironment: [async ({}, use, info) => {
    if (info.project.name.startsWith("app-") && !process.env.FANOUT_LANDING_BASE_URL) {
      const state = JSON.parse(await readFile(process.env.FANOUT_E2E_STATE!, "utf8"));
      Object.assign(process.env, state.env);
    }
    await use();
  }, { scope: "worker", auto: true }],
  baseURL: async ({ appEnvironment: _ }, use, info) => {
    await use(info.project.name.startsWith("app-") ? process.env.FANOUT_E2E_BASE_URL : `http://127.0.0.1:${process.env.E2E_PORT}`);
  },
  storageState: async ({ appEnvironment: _ }, use, info) => {
    await use(info.project.name.startsWith("app-") ? process.env.FANOUT_E2E_STORAGE_STATE : undefined);
  },
});
