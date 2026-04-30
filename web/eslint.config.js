import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import tseslint from "typescript-eslint";

const BANNED_CLASSNAMES = [
  {
    selector:
      "JSXAttribute[name.name='className'] Literal[value=/(?:^|\\s)(?:text|bg|border|ring|fill|stroke|from|to|via)-\\[#[0-9a-fA-F]/]",
    message:
      "Use semantic tokens (text-success, bg-danger/10, border-warning/60, …) instead of hex literals.",
  },
  {
    selector:
      "JSXAttribute[name.name='className'] Literal[value=/(?:^|\\s)transition-all(?:\\s|$)/]",
    message:
      "transition-all causes jank by animating every property. Use transition-colors / transition-opacity / transition-transform / transition-shadow instead.",
  },
  {
    selector:
      "JSXOpeningElement[name.name='button'] JSXAttribute[name.name='className'] Literal[value=/(?:^|\\s)cursor-pointer(?:\\s|$)/]",
    message: "<button> already has cursor: pointer. Drop cursor-pointer.",
  },
  {
    selector:
      "JSXOpeningElement[name.name='a'] JSXAttribute[name.name='className'] Literal[value=/(?:^|\\s)cursor-pointer(?:\\s|$)/]",
    message: "<a> already has cursor: pointer. Drop cursor-pointer.",
  },
];

export default tseslint.config(
  { ignores: ["dist", "vite.config.ts", "eslint.config.js"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true },
      ],
      "@typescript-eslint/consistent-type-imports": [
        "error",
        { prefer: "type-imports", fixStyle: "inline-type-imports" },
      ],
      "@typescript-eslint/no-non-null-assertion": "error",
      "@typescript-eslint/no-explicit-any": "error",
      "no-restricted-syntax": ["error", ...BANNED_CLASSNAMES],
    },
  },
);
