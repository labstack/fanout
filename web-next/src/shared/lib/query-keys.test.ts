import { describe, it, expect } from "vitest";
import { keys } from "./query-keys";

describe("query keys", () => {
  it("includes window and namespace, excludes any token", () => {
    expect(keys.overview(60, "prod")).toEqual(["overview", 60, "prod"]);
  });
  it("uses empty-string namespace for the default", () => {
    expect(keys.overview(60, "")).toEqual(["overview", 60, ""]);
  });
});
