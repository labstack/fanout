import { Badge, Button, MantineProvider, Progress, Table } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { fanoutTheme, fanoutCssVariables } from "../../theme";

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
    expect(remove.style.getPropertyValue("--button-color")).toBe("var(--labstack-color-bad-contrast)");
    expect(save.style.getPropertyValue("--button-color")).toBe("var(--labstack-color-brand-contrast)");
    expect(plain.style.getPropertyValue("--button-color")).toBe("var(--mantine-color-white)");
    await act(async () => root.unmount());
  });

  it("puts token stripe and hover variables on the table", async () => {
    const { root, container } = await render(<Table striped highlightOnHover><Table.Tbody><Table.Tr><Table.Td>Value</Table.Td></Table.Tr></Table.Tbody></Table>);
    const table = container.querySelector("table")!;
    expect(table.style.getPropertyValue("--table-striped-color")).toBe("var(--labstack-inset)");
    expect(table.style.getPropertyValue("--table-highlight-on-hover-color")).toBe("var(--labstack-hover)");
    await act(async () => root.unmount());
  });

  it("keeps every badge at the 12px UI text floor", async () => {
    const { root, container } = await render(<>{["xs", "sm", "md"].map(size => <Badge key={size} size={size}>{size}</Badge>)}</>);
    for (const badge of container.querySelectorAll<HTMLElement>(".mantine-Badge-root")) expect(badge.style.getPropertyValue("--badge-fz")).toBe("calc(0.75rem * var(--mantine-scale))");
    await act(async () => root.unmount());
  });

  it("colours a progress label where Progress cannot overwrite it", async () => {
    // The shared theme owns the label contrast; no local style override.
    const { root, container } = await render(
      <Progress.Root>
        <Progress.Section value={100} color="bad">
          <Progress.Label>3</Progress.Label>
        </Progress.Section>
      </Progress.Root>,
    );
    const section = container.querySelector<HTMLElement>(".mantine-Progress-section")!;
    const label = container.querySelector<HTMLElement>(".mantine-Progress-label")!;
    expect(section.style.getPropertyValue("--progress-label-color")).toBe("var(--labstack-color-bad-contrast)");
    expect(label).not.toBeNull();
    await act(async () => root.unmount());
  });
});
