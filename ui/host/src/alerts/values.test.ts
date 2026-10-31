import { describe, expect, it } from "vitest";
import { formatValue, percentMistake, variables } from "./values";

describe("alert values", () => {
  it("reads variables in order, outside string literals", () => {
    expect(variables("service == \"p95\" && log_count > 1000 && throughput_delta < -50")).toEqual(["log_count", "throughput_delta"]);
    expect(variables("error_rate > 0.1 && error_rate < 0.5")).toEqual(["error_rate"]);
  });

  it("formats by variable, keeping zero", () => {
    expect(formatValue("error_rate", 0.0925)).toBe("9.25%");
    expect(formatValue("p95", 412.345)).toBe("412.35 ms");
    expect(formatValue("log_count", 35712)).toBe("35,712");
    expect(formatValue("throughput_delta", -60)).toBe("-60%");
    expect(formatValue("p95_delta", 12.5)).toBe("+12.5%");
    expect(formatValue("throughput", 0)).toBe("0");
  });

  it("warns when error_rate is compared with a percent", () => {
    expect(percentMistake("error_rate > 5")).toMatch(/0 to 1.*write 0\.05/);
    expect(percentMistake("5 < error_rate")).toMatch(/0 to 1/);
    expect(percentMistake("error_rate > 0.05 && throughput > 10")).toBeUndefined();
    expect(percentMistake("error_rate_delta > 50")).toBeUndefined();
  });
});
