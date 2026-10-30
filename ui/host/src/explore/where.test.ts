import { describe, expect, it } from "vitest";
import { operators, parseWhere, printWhere } from "./where";

describe("where entries", () => {
  it("round-trips each operator", () => {
    for (const op of operators) {
      const entry = `duration_ms ${op} 500`;
      expect(parseWhere(entry)).toEqual({ field: "duration_ms", op, value: "500" });
      expect(printWhere(parseWhere(entry)!, true)).toBe(entry);
    }
  });

  it("quotes strings and doubles single quotes", () => {
    expect(printWhere({ field: "service", op: "=", value: "O'Brien" }, false)).toBe("service = 'O''Brien'");
    expect(parseWhere("service = 'O''Brien'")).toEqual({ field: "service", op: "=", value: "O'Brien" });
    expect(printWhere({ field: "duration_ms", op: ">", value: "fast" }, true)).toBe("duration_ms > 'fast'");
    expect(printWhere({ field: "http_status_code", op: "=", value: "500" }, false)).toBe("http_status_code = '500'");
  });

  it("reads attribute keys", () => {
    expect(parseWhere("attributes['http.route'] = '/cart'")).toEqual({ field: "attributes['http.route']", op: "=", value: "/cart" });
  });

  it("leaves variables, boolean logic and functions alone", () => {
    expect(parseWhere("service = $service")).toBeUndefined();
    expect(parseWhere("service = 'a' OR service = 'b'")).toBeUndefined();
    expect(parseWhere("contains(body, 'timeout')")).toBeUndefined();
    expect(parseWhere("service IN ('a', 'b')")).toBeUndefined();
  });
});
