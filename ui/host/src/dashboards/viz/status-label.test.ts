import { expect, it } from "vitest";
import { statusLabel } from "./status-label";

it("sentence-cases status words and leaves formatted values and codes as given", () => {
  expect(statusLabel("ERROR")).toBe("Error");
  expect(statusLabel("OK")).toBe("OK");
  expect(statusLabel("degraded")).toBe("Degraded");
  expect(statusLabel("1.5 KiB")).toBe("1.5 KiB");
  expect(statusLabel("1.2K/s")).toBe("1.2K/s");
  expect(statusLabel("HTTP 503")).toBe("HTTP 503");
});
