import { describe, expect, it } from "vitest";
import { duration, exactTimestamp, percent, timeZoneLabel } from "../../format";

describe("duration", () => {
  it("keeps sub-millisecond values legible", () => {
    expect(duration(0)).toBe("0ms");
    expect(duration(0.42)).toBe("0.42ms");
  });

  it("drops decimals as the millisecond value grows", () => {
    expect(duration(48.16)).toBe("48.2ms");
    expect(duration(240)).toBe("240ms");
  });

  it("reads seconds without false precision", () => {
    expect(duration(7650)).toBe("7.65s");
    expect(duration(13691)).toBe("13.7s");
  });

  // "600.00s" was two decimals of false precision on ten minutes, sitting in
  // the same column as "25.0ms".
  it("reads minutes and hours above a minute", () => {
    expect(duration(600_000)).toBe("10m");
    expect(duration(240_020)).toBe("4m");
    expect(duration(263_000)).toBe("4m 23s");
    expect(duration(3_600_000)).toBe("1h");
    expect(duration(5_400_000)).toBe("1h 30m");
  });

  it("never rounds seconds up into a full minute", () => {
    expect(duration(119_600)).toBe("2m");
  });

  it("says nothing rather than something wrong", () => {
    expect(duration(Number.NaN)).toBe("—");
  });
});

describe("percent", () => {
  it("keeps two decimals for small rates and one for large", () => {
    expect(percent(0.0216)).toBe("2.16%");
    expect(percent(0.141)).toBe("14.1%");
  });

  // A rate that rounds to zero beside a red badge reads as a contradiction.
  it("says a small rate is small rather than zero", () => {
    expect(percent(0.00002)).toBe("<0.01%");
    expect(percent(0)).toBe("0.00%");
  });
});

describe("duration seams", () => {
  // 999.5ms used to print "1000ms": four digits of milliseconds sitting in a
  // column of seconds.
  it("never rounds up into the unit it just left", () => {
    expect(duration(999.5)).toBe("1.00s");
    expect(duration(999.9)).toBe("1.00s");
    expect(duration(99.96)).toBe("100ms");
    expect(duration(59_950)).toBe("1m");
    expect(duration(3_599_600)).toBe("1h");
  });

  // A microsecond span is not a zero-length one, for the same reason a rate of
  // 0.002% is not no errors.
  it("says a tiny duration is tiny rather than zero", () => {
    expect(duration(0.004)).toBe("<0.01ms");
    expect(duration(0)).toBe("0ms");
  });

  it("shows a negative duration rather than hiding it as missing", () => {
    expect(duration(-5)).toBe("-5.0ms");
  });
});

describe("time zone", () => {
  // Every timestamp is rendered in the browser's zone and none of them said
  // so, which is ambiguous the moment two people read the same incident from
  // different offices.
  it("names the viewer's zone and spells an instant out in full", () => {
    expect(timeZoneLabel(new Date("2026-09-07T14:00:00Z"))).not.toBe("");
    // An abbreviation belongs to an instant, not to a zone: a table of rows
    // from before a daylight-saving change must not be headed with today's.
    expect(timeZoneLabel("2026-09-07T14:00:00Z")).toBe(timeZoneLabel(new Date("2026-09-07T14:00:00Z")));
    expect(timeZoneLabel("not a time")).toBe(timeZoneLabel());
    const exact = exactTimestamp("2026-09-07T14:00:00Z");
    expect(exact).toContain("2026");
    expect(exact).toMatch(/\d{1,2}:\d{2}:\d{2}/);
    expect(exact).toContain(timeZoneLabel(new Date("2026-09-07T14:00:00Z")));
    // A value that is not a time is passed through rather than shown as
    // "Invalid Date".
    expect(exactTimestamp("not a time")).toBe("not a time");
    // Callers hold epochs and Dates as often as strings, and a round-trip
    // through toISOString throws where this returns the value unchanged.
    expect(exactTimestamp(Date.parse("2026-09-07T14:00:00Z"))).toBe(exact);
    expect(exactTimestamp(new Date("2026-09-07T14:00:00Z"))).toBe(exact);
  });
});
