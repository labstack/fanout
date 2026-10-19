import { expect, test } from "../../fixtures/test";
import { mkdir, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { cardSelector, readyCard, settle } from "../../support/accessibility-support";
import { recordingTimeline, revealRecording, waitForConversation } from "../../support/landing-recording-support";
import type { DashboardSpec, PanelResult } from "../../../../panels/types";

const baseURL = process.env.FANOUT_LANDING_BASE_URL;
const directory = fileURLToPath(new URL("../../../test-results/landing-recordings/", import.meta.url));
test.skip(process.env.FANOUT_LANDING_RECORDINGS !== "1", "set FANOUT_LANDING_RECORDINGS=1 to record real landing clips");
test.setTimeout(8 * 60_000);
test.use({
  storageState: async ({}, use) => {
    if (!baseURL) throw new Error("Landing recordings require FANOUT_LANDING_BASE_URL for an external instance");
    const match = /^([!#$%&'*+.^_`|~\w-]+)=([^;\s]+)$/.exec(process.env.FANOUT_LANDING_SESSION_COOKIE ?? "");
    if (!match) throw new Error("FANOUT_LANDING_SESSION_COOKIE must be a nonempty name=value session cookie");
    const url = new URL(baseURL);
    await use({ cookies: [{ name: match[1], value: match[2], domain: url.hostname, path: "/", expires: -1,
      httpOnly: true, secure: url.protocol === "https:", sameSite: "Lax" }], origins: [] });
  },
});

const prompts = {
  chat: "Why is checkout slower in the last hour?",
  dashboard: "Build a dashboard for checkout: headline stats and trends for request rate, error rate and p95 latency, p95 by operation, slowest dependencies, and recent slow traces",
};

for (const view of ["chat", "dashboard"] as const) {
  test(`record ${view} on the OpenTelemetry Demo`, async ({ browser, storageState }, info) => {
    test.skip(info.project.use.viewport?.width !== 1440, "use the 1440px projects to record each theme once at 1280 × 800 CSS pixels");
    const theme = info.project.use.colorScheme as "light" | "dark";
    await mkdir(directory, { recursive: true });
    const context = await browser.newContext({ baseURL, storageState, colorScheme: theme,
      viewport: { width: 1280, height: 800 },
      recordVideo: { dir: directory, size: { width: 1280, height: 800 } } });
    try {
      const status = await context.request.get("/api/auth/status");
      expect(status.ok(), "Cannot read runtime status from the external instance").toBe(true);
      if (!(await status.json() as { agent_available: boolean }).agent_available) {
        throw new Error("Chat is disabled on the external instance; configure an AI provider before recording landing clips");
      }
      await context.addInitScript(value => localStorage.setItem("mantine-color-scheme-value", value), theme);
      const startedAt = Date.now();
      const page = await context.newPage();
      const video = page.video()!;
      await page.goto("/chat");
      // Use this page's session and the owner-scoped APIs to clear earlier takes.
      await page.evaluate(async () => {
        const request = async (path: string, method = "GET", headers: Record<string, string> = {}) => {
          const response = await fetch(path, { method, credentials: "same-origin", redirect: "error",
            headers: { "Fanout-Request": "1", ...headers } }).catch(() => {
            throw new Error(`Landing recording cleanup failed: ${method} ${path} (request could not complete)`);
          });
          if (!response.ok) throw new Error(`Landing recording cleanup failed: ${method} ${path} (HTTP ${response.status})`);
          return response;
        };
        // Finish pagination before deleting so removals cannot affect the scan.
        const threadIds: string[] = [];
        let cursor: string | null = null;
        do {
          const params = new URLSearchParams({ page_size: "100" });
          if (cursor) params.set("cursor", cursor);
          const response = await request(`/api/agent/threads?${params}`);
          const page = await response.json() as { items: { id: string }[]; next_cursor: string | null };
          threadIds.push(...page.items.map(thread => thread.id));
          cursor = page.next_cursor;
        } while (cursor);
        for (const id of threadIds) await request(`/api/agent/threads/${encodeURIComponent(id)}`, "DELETE");

        const response = await request("/api/dashboards");
        const { dashboards } = await response.json() as { dashboards: { id: string; is_default: boolean }[] };
        for (const dashboard of dashboards) {
          // Only an explicit non-default flag permits deletion; never touch the default.
          if (dashboard.is_default !== false) continue;
          await request(`/api/dashboards/${encodeURIComponent(dashboard.id)}`, "DELETE",
            { "Fanout-Confirm-Delete": dashboard.id });
        }
      });
      await page.reload();
      const navigation = page.getByRole("navigation", { name: "Navigation", exact: true });
      await expect(navigation.getByText("No chats yet", { exact: true })).toBeVisible();
      await expect(navigation.locator('a[href^="/dashboards/"]')).toHaveCount(1);
      await expect(navigation.locator('a[href^="/dashboards/"]').getByText("System overview", { exact: true })).toBeVisible();
      await expect(page.locator("html")).toHaveAttribute("data-mantine-color-scheme", theme);
      const input = page.getByRole("textbox", { name: "Message Fanout", exact: true });
      await expect(input, "Chat must be enabled and the session must allow agent runs").toBeEnabled();
      // The clip starts on the settled empty chat, not on page load and cleanup.
      const start = (Date.now() - startedAt) / 1000;
      await input.click();
      // A steady human typing cadence, including a short pause at punctuation.
      for (const character of prompts[view]) {
        await input.pressSequentially(character, { delay: /[,:?]/.test(character) ? 120 : 28 });
      }
      const responseReady = page.waitForResponse(response => new URL(response.url()).pathname === "/api/agent/runs"
        && response.request().method() === "POST", { timeout: 30_000 });
      await page.getByRole("button", { name: "Send message", exact: true }).click();
      const response = await responseReady;
      const responseAt = (Date.now() - startedAt) / 1000;
      expect(response.ok(), "Agent request failed; check chat configuration and the session's agent:run capability").toBe(true);
      const stream = await response.text();
      const timeline = recordingTimeline(stream, responseAt);
      // Check every evidence view, including collapsed views without a mounted iframe.
      const evidence = stream.split(/\r?\n/).filter(line => line.startsWith("data:")).map(line => JSON.parse(line.slice(5)) as {
        type: string; messageId?: string; activityType?: string;
        content?: { tool_result?: { view: { key: string }; results: PanelResult[] } };
      }).filter(event => event.type === "ACTIVITY_SNAPSHOT" && event.activityType === "mcp-app")
        .map(event => event.content!.tool_result!);
      if (view === "chat") expect(new Set(evidence.map(fragment => fragment.view.key)).size, "Chat must show at least four evidence views").toBeGreaterThanOrEqual(4);
      for (const fragment of evidence) {
        expect(fragment.results.some(result => result.status === "ok" && (result.frame?.rows ?? 0) > 0),
          "Chat must not contain an empty evidence view").toBe(true);
      }
      const conversationWaitStart = (Date.now() - startedAt) / 1000;
      // RUN_FINISHED in the real stream plus the UI's idle state gates the reveal.
      await expect(page.getByRole("button", { name: "Stop", exact: true })).toHaveCount(0);
      await expect(page.locator(".chat-pane .mantine-Loader-root")).toHaveCount(0);
      await expect(page.locator(".chat-pane").getByRole("alert")).toHaveCount(0);
      await expect(page.locator(".chat-pane").getByText(/^\d+ steps? failed$/), "The run must not show failed steps").toHaveCount(0);
      await expect(page.locator('[data-answer-position] [data-role="assistant"]')).toBeVisible();
      const views = page.locator('iframe[title="Fanout analysis view"]');
      for (let index = 0; index < await views.count(); index++) {
        const frame = views.nth(index).contentFrame();
        await expect.poll(() => frame.locator("html").evaluate(element => element.ownerDocument.readyState)).toBe("complete");
        await expect(frame.locator("[data-panel-body]").first()).toBeAttached();
        await expect(frame.locator(".mantine-Loader-root, [data-panel-error]")).toHaveCount(0);
      }
      await settle(page);
      await waitForConversation(page);
      timeline.waits.push({ kind: "conversation_ready", start: conversationWaitStart, end: (Date.now() - startedAt) / 1000 });

      if (view === "dashboard") {
        if (!timeline.dashboardId) throw new Error("No successful create_dashboard result with a committed save receipt; the recording requires a newly saved dashboard");
        const receipt = page.locator(`[data-dashboard-result="${timeline.dashboardId}"]`);
        const dashboardWaitStart = (Date.now() - startedAt) / 1000;
        await receipt.getByRole("link", { name: "Open dashboard", exact: true }).click();
        await expect(page).toHaveURL(new RegExp(`/dashboards/${timeline.dashboardId}(?:\\?|$)`));
        const saved = await context.request.get(`/api/dashboards/${encodeURIComponent(timeline.dashboardId)}`);
        expect(saved.ok()).toBe(true);
        const { spec } = await saved.json() as { spec: DashboardSpec };
        expect(spec.panels.length, "The created dashboard must contain at least six panels").toBeGreaterThanOrEqual(6);
        const preview = await context.request.post("/api/panels/query", {
          headers: { "Fanout-Request": "1" }, data: { dashboard: spec },
        });
        expect(preview.ok()).toBe(true);
        const { results } = await preview.json() as { results: PanelResult[] };
        for (const panel of spec.panels.filter(panel => panel.viz !== "text")) {
          const result = results.find(result => result.id === panel.id);
          expect(result?.status, `${panel.title} must return data`).toBe("ok");
          expect(result?.frame?.rows ?? 0, `${panel.title} must have rows`).toBeGreaterThan(0);
        }
        for (const panel of spec.panels) await readyCard(page, panel);
        await page.evaluate(() => window.scrollTo(0, 0));
        // Lazy panels returning to the first fold must be settled too.
        for (const panel of spec.panels) {
          const card = page.locator(cardSelector(panel.id));
          await expect(card.locator('[aria-label="Loading panel"], [aria-label="Refreshing"], [data-panel-error]')).toHaveCount(0);
        }
        await settle(page);
        timeline.waits.push({ kind: "dashboard_ready", start: dashboardWaitStart, end: (Date.now() - startedAt) / 1000 });
      }
      await page.mouse.move(0, 0);
      const holdStart = (Date.now() - startedAt) / 1000;
      await revealRecording(page, view);
      timeline.waits.push({ kind: "hold", start: holdStart, end: (Date.now() - startedAt) / 1000 });
      if (view === "chat") {
        const visible = await page.locator("[data-answer-position]").evaluate(answer => {
          const pane = answer.closest(".chat-scroll")!;
          const top = pane.getBoundingClientRect().top + pane.clientTop;
          const bottom = top + pane.clientHeight;
          const markdown = answer.querySelector(".chat-markdown")!;
          const walker = document.createTreeWalker(markdown, NodeFilter.SHOW_TEXT);
          let last: Node | undefined;
          for (let node = walker.nextNode(); node; node = walker.nextNode()) if (node.textContent?.trim()) last = node;
          const range = document.createRange();
          if (last) range.selectNodeContents(last);
          const line = [...range.getClientRects()].at(-1);
          return { aligned: Math.abs(answer.getBoundingClientRect().bottom - bottom) <= 2,
            lastLineVisible: !!line && line.top >= top && line.bottom <= bottom + 1 };
        });
        expect(visible, "The final answer end must stay at the pane bottom with its last line in view").toEqual({ aligned: true, lastLineVisible: true });
      }
      const duration = (Date.now() - startedAt) / 1000;
      await context.close();
      const rawFile = `${view}-${theme}.webm`;
      await video.saveAs(`${directory}${rawFile}`);
      await video.delete();
      // No cookies, request bodies, tool results or telemetry are persisted here.
      await writeFile(`${directory}${view}-${theme}.json`, JSON.stringify({ version: 1, raw_file: rawFile,
        viewport: { width: 1280, height: 800 }, theme, prompt: prompts[view], start, duration,
        clock: "seconds from page creation; server event deltas anchored to response headers",
        waits: timeline.waits }, null, 2) + "\n");
    } finally {
      await context.close();
    }
  });
}
