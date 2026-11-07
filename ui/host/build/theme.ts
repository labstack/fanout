// Shared UI sources resolve the host's single installed theme dependency.
export const themeAlias = [
  { find: /^@labstack\/ui$/, replacement: new URL("../node_modules/@labstack/ui/dist/index.js", import.meta.url).pathname },
];
