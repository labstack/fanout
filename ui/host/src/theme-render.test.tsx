import { Badge, Button, MantineProvider, Progress } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { fanoutCssVariables } from "../../theme";
import { typeScale } from "../../tokens";
import { fanoutTheme } from "./theme";

/* These render the real components against the real theme, because the two
   design-system defects that reached this branch were both invisible to a
   test of the configuration: a variable can be declared correctly and still
   never reach the element, and a component can carry its own scale that the
   theme was not consulted about. */
async function render(node: React.ReactNode) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => root.render(<MantineProvider theme={fanoutTheme} cssVariablesResolver={fanoutCssVariables} forceColorScheme="dark">{node}</MantineProvider>));
  return { root, container };
}

describe("what actually lands on the element", () => {
  afterEach(() => { document.body.innerHTML = ""; });

  it("draws a filled surface's text from this theme's per-scheme contrast", async () => {
    const { root, container } = await render(<>
      <Button color="bad">Remove</Button>
      <Button>Save</Button>
      <Button color="gray" autoContrast={false}>Plain</Button>
    </>);
    const [remove, save, plain] = [...container.querySelectorAll<HTMLButtonElement>("button")];
    // White on bad's dark fill #f26d78 measures 2.90:1.
    expect(remove.style.getPropertyValue("--button-color")).toBe("var(--fanout-color-bad-contrast)");
    expect(save.style.getPropertyValue("--button-color")).toBe("var(--fanout-color-brand-contrast)");
    expect(plain.style.getPropertyValue("--button-color")).toBe("var(--mantine-color-white)");
    await act(async () => root.unmount());
  });

  it("raises only the badge sizes that sit under the floor", async () => {
    const { root, container } = await render(<>
      <Badge size="xs">xs</Badge>
      <Badge size="sm">sm</Badge>
      <Badge size="md">md</Badge>
      <Badge size="lg">lg</Badge>
      <Badge size="1.5rem">custom</Badge>
    </>);
    const [xs, sm, md, lg, custom] = [...container.querySelectorAll<HTMLElement>(".mantine-Badge-root")];
    const floor = `${typeScale.micro / 16}rem`;
    // Mantine's own xs is 9px and sm is 10px.
    expect(xs.style.getPropertyValue("--badge-fz")).toBe(floor);
    expect(sm.style.getPropertyValue("--badge-fz")).toBe(floor);
    // md is already 11px and lg is 13px, so both keep Mantine's own size, and
    // a size the scale does not name is not rewritten at all.
    expect(md.style.getPropertyValue("--badge-fz")).toBe("var(--badge-fz-md)");
    expect(lg.style.getPropertyValue("--badge-fz")).toBe("var(--badge-fz-lg)");
    expect(custom.style.getPropertyValue("--badge-fz")).not.toBe(floor);
    await act(async () => root.unmount());
  });

  it("colours a progress label where Progress cannot overwrite it", async () => {
    // Progress sets --progress-label-color through its own vars, which are
    // written after any style prop on the section: pointing that variable
    // elsewhere from the section does nothing, and the count stayed white on
    // #f26d78 at 2.90:1.
    const { root, container } = await render(
      <Progress.Root>
        <Progress.Section value={100} color="bad" style={{ "--progress-label-color": "var(--fanout-color-bad-contrast)" } as React.CSSProperties}>
          <Progress.Label style={{ color: "var(--fanout-color-bad-contrast)" }}>3</Progress.Label>
        </Progress.Section>
      </Progress.Root>,
    );
    const section = container.querySelector<HTMLElement>(".mantine-Progress-section")!;
    const label = container.querySelector<HTMLElement>(".mantine-Progress-label")!;
    expect(section.style.getPropertyValue("--progress-label-color")).toBe("var(--mantine-color-white)");
    expect(label.style.color).toBe("var(--fanout-color-bad-contrast)");
    await act(async () => root.unmount());
  });
});
