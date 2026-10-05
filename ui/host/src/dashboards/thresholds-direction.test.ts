import { describe, expect, it } from "vitest";
import { statusFor } from "../../../panels/thresholds";

describe("statusFor without a direction", () => {
  it("uses a single unhealthy threshold when direction is absent", () => {
    expect(statusFor(100, [{ value: 50, status: "bad" }])).toBe("bad");
    expect(statusFor(100, [{ value: 50, status: "bad" }], "lower")).toBe("bad");
    expect(statusFor(100, [{ value: 50, status: "bad" }], "higher")).toBe("ok");
  });
  it("derives direction from severity order independently of input order", () => {
    expect(statusFor(120, [{ value: 50, status: "warn" }, { value: 100, status: "bad" }])).toBe("bad");
    expect(statusFor(20, [{ value: 50, status: "warn" }, { value: 10, status: "bad" }])).toBe("warn");
    expect(statusFor(5, [{ value: 10, status: "bad" }, { value: 50, status: "warn" }])).toBe("bad");
  });
});
