import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Product, pageParameters } from "./catalog/product";
const meta = { id: "pages-sign-in", title: "Pages/Sign in", component: Product, parameters: { ...pageParameters, docs: { ...pageParameters.docs, description: { component: "The real local sign-in and first-run setup screens. Authentication requests stay inside the fictional fetch service." } }, scenario: { signedOut: true } } } satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
const enterCode: NonNullable<Story["play"]> = async ({ canvas, userEvent }) => {
  await userEvent.type(await canvas.findByRole("textbox", { name: "Email" }), "alex@example.test");
  await userEvent.click(canvas.getByRole("button", { name: "Continue with email" }));
  await expect(await canvas.findByRole("heading", { name: "Check your email" })).toBeVisible();
  const address = canvas.getByText("alex@example.test");
  await expect(getComputedStyle(address).fontWeight).toBe("700");
  const digit = canvas.getByRole("textbox", { name: "Verification code, digit 1 of 6" });
  await expect(digit).toHaveAttribute("autocomplete", "one-time-code");
  await expect(canvas.getByText(/Codes expire in 5 minutes/)).toBeVisible();
  await expect(canvas.queryByRole("textbox", { name: "Email" })).toBeNull();
};
export const Code: Story = { play: enterCode };
export const Error: Story = { parameters: { scenario: { signedOut: true, signInError: true } }, play: async context => {
  await enterCode(context);
  for (let i = 1; i <= 6; i++) await context.userEvent.type(context.canvas.getByRole("textbox", { name: `Verification code, digit ${i} of 6` }), String(i));
  await expect(await context.canvas.findByRole("alert")).toHaveTextContent("Invalid or expired code");
  await expect(context.canvas.getByRole("textbox", { name: "Verification code, digit 1 of 6" })).toHaveFocus();
  await expect(context.canvas.getByRole("textbox", { name: "Verification code, digit 1 of 6" })).toHaveValue("");
} };
export const Email: Story = { play: async ({ canvas, userEvent }) => {
  const email = await canvas.findByRole("textbox", { name: "Email" });
  await expect(canvas.getByRole("heading", { name: "Sign in to Fanout" })).toBeVisible();
  await expect(canvas.getByText(/We'll email you a 6-digit code\./)).toBeVisible();
  await expect(canvas.getByRole("link", { name: "Docs" })).toHaveAttribute("href", "https://fanout.run/docs");
  await expect(email).toBeRequired();
  await expect(email).toHaveAttribute("autocomplete", "email");
  await userEvent.click(canvas.getByRole("button", { name: "Continue with email" }));
  await expect(email).toBeInvalid();
  await expect(canvas.queryByRole("heading", { name: "Check your email" })).toBeNull();
} };
export const FirstRun: Story = { parameters: { scenario: { setup: true } }, play: async ({ canvas }) => { await expect(await canvas.findByRole("textbox", { name: "Email" })).toBeVisible(); } };
