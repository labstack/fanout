import type { Plugin } from "vite";

// Catalog fixtures and stories are local review inputs, never app assets.
export function excludeCatalog(): Plugin {
  return {
    name: "exclude-product-catalog",
    apply: "build",
    generateBundle() {
      for (const id of this.getModuleIds()) {
        if (/\/src\/catalog\/|\.stories\.[jt]sx?(?:\?|$)|\/\.storybook\//.test(id)) this.error(`Catalog module entered the production build: ${id}`);
      }
    },
  };
}
