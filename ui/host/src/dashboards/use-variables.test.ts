import { describe, expect, it } from "vitest";
import { ALL, type Variable } from "../../../panels/types";
import { currentValue } from "./variable-bar";

const variable: Variable = { name: "service", kind: "query" };
const options = [{ value: "checkout" }, { value: "cart" }];

describe("variable value selection", () => {
  it("treats an empty single selection as unset before options load", () => {
    expect(currentValue({ ...variable, default: "cart" }, { service: [] })).toBe("cart");
    expect(currentValue(variable, { service: [] }, options)).toBe("checkout");
    expect(currentValue(variable, { service: [] })).toBe("");
    expect(currentValue({ ...variable, multi: true }, { service: [] })).toEqual([]);
  });
  it("falls back from All without include_all to the default, then the first option", () => {
    expect(currentValue({ ...variable, default: "cart" }, { service: ALL }, options)).toBe("cart");
    expect(currentValue(variable, { service: ALL }, options)).toBe("checkout");
  });

  it("falls back from a single value missing from loaded options", () => {
    expect(currentValue(variable, { service: "missing" }, options)).toBe("checkout");
    expect(currentValue({ ...variable, default: "cart" }, { service: "missing" }, options)).toBe("cart");
    expect(currentValue({ ...variable, include_all: true }, { service: "missing" }, options)).toBe(ALL);
  });

  it("drops invalid multi values and falls back when none remain", () => {
    expect(currentValue({ ...variable, multi: true }, { service: ["missing", "cart"] }, options)).toEqual(["cart"]);
    expect(currentValue({ ...variable, multi: true }, { service: ["missing"] }, options)).toBe("checkout");
    expect(currentValue({ ...variable, multi: true }, { service: [] }, options)).toEqual([]);
  });

  it("uses server precedence and preserves provided values until options load", () => {
    expect(currentValue({ ...variable, kind: "constant", value: "fixed" }, { service: "cart" }, options)).toBe("fixed");
    expect(currentValue({ ...variable, default: "cart", include_all: true }, {}, options)).toBe("cart");
    expect(currentValue({ ...variable, include_all: true }, {}, options)).toBe(ALL);
    expect(currentValue(variable, {}, options)).toBe("checkout");
    expect(currentValue(variable, { service: "cart" })).toBe("cart");
    expect(currentValue(variable, { service: "cart" }, [])).toBe("");
    expect(currentValue({ name: "text", kind: "text" }, { text: "free text" }, [])).toBe("free text");
    expect(currentValue({ ...variable, kind: "custom", options: ["checkout"] }, { service: "missing" })).toBe("checkout");
  });
});
