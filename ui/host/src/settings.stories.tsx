import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within, waitFor } from "storybook/test";
import { ingestToken } from "./catalog/fixtures";
import { Product, pageParameters } from "./catalog/product";
const meta = { id: "pages-settings", title: "Pages/Settings", component: Product, args: { path: "/settings" }, parameters: { ...pageParameters, docs: { ...pageParameters.docs, description: { component: "The shipping telemetry connection screen, with a fictional TLS endpoint, ingest-token status, and connected agent." } }, } } satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Connected: Story = { play: async ({ canvas }) => {
  await expect(await canvas.findByRole("heading", { name: "Connect telemetry" })).toBeVisible();
  await expect(await canvas.findByText("Operations assistant")).toBeVisible();
} };

export const RotatedToken: Story = { play: async ({ canvas, userEvent }) => {
  await userEvent.click(await canvas.findByRole("button", { name: "Rotate token" }));
  const dialog = within(await within(document.body).findByRole("dialog", { name: "Rotate the ingest token?" }));
  await userEvent.click(dialog.getByRole("button", { name: "Rotate token" }));
  await waitFor(() => expect(canvas.getByText(`authorization: Bearer ${ingestToken}`, { exact: true })).toBeVisible());
  await waitFor(() => expect(document.querySelector(".mantine-Modal-content")).not.toBeInTheDocument());
} };

export const IdentityProviderRoles: Story = { parameters: { scenario: { accountOwnership: "identity_provider_roles" } }, play: async ({ canvas }) => {
  await expect(await canvas.findByText("Managed by your identity provider")).toBeVisible();
} };

export const IdentityProviderDirectory: Story = { parameters: { scenario: { accountOwnership: "identity_provider_directory" } }, play: async ({ canvas }) => {
  await expect(await canvas.findByText("Managed by your identity provider")).toBeVisible();
} };

export const ChatDisabled: Story = { parameters: { scenario: { chatDisabled: true } }, play: async ({ canvas }) => {
  await expect(await canvas.findByRole("heading", { name: "Chat is disabled" })).toBeVisible();
} };
