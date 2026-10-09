import type { FullResult, Reporter, TestCase, TestError, TestResult } from "@playwright/test/reporter";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { join, relative, resolve } from "node:path";

const escape = (text: string) => text.replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);

// A small standalone HTML reporter: passing runs never write a report.
export default class FailureReport implements Reporter {
  private rows: string[] = [];
  private output: string;
  constructor(options: { outputFolder?: string } = {}) {
    this.output = resolve(options.outputFolder ?? "playwright-report");
  }
  onTestEnd(test: TestCase, result: TestResult) {
    const attachments = result.attachments.filter(a => a.path).map(a =>
      `<a href="${escape(relative(this.output, a.path!).split("\\").join("/"))}">${escape(a.name)}</a>`).join(" ");
    this.rows.push(`<article><h2>${escape(test.titlePath().join(" / "))}</h2><p>${escape(result.status)} · ${result.duration} ms</p>${result.errors.map(e => `<pre>${escape(e.stack ?? e.message ?? "Unknown error")}</pre>`).join("")}${attachments}</article>`);
  }
  onError(error: TestError) { this.rows.push(`<pre>${escape(error.stack ?? error.message ?? "Unknown error")}</pre>`); }
  async onEnd(result: FullResult) {
    await rm(this.output, { recursive: true, force: true });
    if (result.status === "passed") return;
    await mkdir(this.output, { recursive: true });
    await writeFile(join(this.output, "index.html"), `<!doctype html><meta charset="utf-8"><title>Fanout browser smoke</title><style>body{font:16px system-ui;max-width:1100px;margin:32px auto;padding:0 24px}pre{white-space:pre-wrap}article{border-bottom:1px solid #888;padding:16px 0}</style><h1>Fanout browser smoke: ${escape(result.status)}</h1>${this.rows.join("\n")}`);
  }
}
