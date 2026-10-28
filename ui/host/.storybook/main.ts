import type { StorybookConfig } from "@storybook/react-vite";

// Local review only, never published.
const config: StorybookConfig = {
  stories: ["../src/**/*.stories.tsx"],
  framework: "@storybook/react-vite",
  core: { disableTelemetry: true },
  features: { sidebarOnboardingChecklist: false, menuOnboardingChecklist: false },
  staticDirs: [{ from: "../node_modules/.cache/fanout-catalog-apps", to: "/catalog-apps" }],
  addons: ["@storybook/addon-docs", "@storybook/addon-a11y", "@storybook/addon-vitest"],
  viteFinal: vite => ({
    ...vite,
    base: "/",
    plugins: (vite.plugins ?? []).filter(plugin => !plugin || typeof plugin !== "object" || !("name" in plugin) || !/tanstack|exclude-product-catalog/.test(String(plugin.name))),
    server: { ...vite.server, proxy: {}, fs: { allow: ["../.."] } },
    optimizeDeps: { ...vite.optimizeDeps, include: [...(vite.optimizeDeps?.include ?? []), "react", "react-dom", "react-dom/client", "@mantine/core", "@mantine/hooks", "storybook/internal/core-events", "@storybook/react-vite", "@tanstack/react-query", "@tanstack/react-router", "react-markdown", "remark-gfm", "@phosphor-icons/react", "react-grid-layout", "@modelcontextprotocol/ext-apps/app-bridge", "@modelcontextprotocol/client"] },
  }),
};

export default config;
