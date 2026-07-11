import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import jsxA11y from "eslint-plugin-jsx-a11y";
import tseslint from "typescript-eslint";

const BANNED_CLASSNAMES = [
  {
    selector:
      "JSXAttribute[name.name='className'] Literal[value=/(?:^|\\s)(?:text|bg|border|ring|fill|stroke|from|to|via)-\\[#[0-9a-fA-F]/]",
    message:
      "Use semantic tokens (text-ink, bg-crit/10, border-line, …) instead of hex literals.",
  },
];

export default tseslint.config(
  { ignores: ["dist", "vite.config.ts", "vitest.config.ts", "eslint.config.js"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: { ecmaVersion: 2022, globals: globals.browser },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
      "jsx-a11y": jsxA11y,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      ...jsxA11y.flatConfigs.recommended.rules,
      "@typescript-eslint/no-explicit-any": "error",
      "@typescript-eslint/no-non-null-assertion": "error",
      "@typescript-eslint/consistent-type-imports": "error",
      "no-restricted-syntax": ["error", ...BANNED_CLASSNAMES],
    },
  },
);
