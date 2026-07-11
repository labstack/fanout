import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { axe } from "jest-axe";
import { Button } from "./button";

describe("Button", () => {
  it("renders an accessible button with its label", () => {
    render(<Button>Investigate</Button>);
    expect(screen.getByRole("button", { name: "Investigate" })).toBeInTheDocument();
  });
  it("applies the ghost variant class", () => {
    render(<Button variant="ghost">Go</Button>);
    expect(screen.getByRole("button", { name: "Go" }).className).toContain("border-line");
  });
  it("has no axe violations", async () => {
    const { container } = render(<Button>Ok</Button>);
    expect(await axe(container)).toHaveNoViolations();
  });
});
