import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Product, pageParameters } from "./catalog/product";
const meta = { id: "pages-sign-in", title: "Pages/Sign in", component: Product, parameters: { ...pageParameters, docs: { ...pageParameters.docs, description: { component: "The real local sign-in and first-run setup screens. Authentication requests stay inside the fictional fetch service." } }, scenario: { signedOut: true } } } satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Email: Story = { play: async ({ canvas }) => { await expect(await canvas.findByRole("textbox", { name: "Email" })).toBeVisible(); } };
export const FirstRun: Story = { parameters: { scenario: { setup: true } }, play: async ({ canvas }) => { await expect(await canvas.findByRole("textbox", { name: "Email" })).toBeVisible(); } };
