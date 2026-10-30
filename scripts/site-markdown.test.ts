import { expect, test } from "bun:test";
import { toPlainMarkdown } from "../site/src/lib/markdown";

test("documentation cards retain their section titles and links in Markdown", () => {
  const source = `import { Card, CardGrid } from "@astrojs/starlight/components";

<CardGrid>
  <Card title="Get started">

- [Install](/docs/get-started/install)

  </Card>
</CardGrid>`;
  const markdown = toPlainMarkdown(source);
  expect(markdown).toContain("### Get started");
  expect(markdown).toContain("- [Install](/docs/get-started/install)");
  expect(markdown).not.toMatch(/import|<\/?Card/);
});

test("Markdown conversion preserves component examples inside code fences", () => {
  const source = '```mdx\n<Card title="Example">\n\n[Install](/docs/get-started/install)\n\n</Card>\n```';
  expect(toPlainMarkdown(source)).toBe(source);
});
