import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within, waitFor } from "storybook/test";
import { ingestToken } from "./catalog/fixtures";
import { Product, pageParameters } from "./catalog/product";
const meta = { id: "pages-settings", title: "Pages/Settings", component: Product, args: { path: "/settings" }, parameters: { ...pageParameters, docs: { ...pageParameters.docs, description: { component: "The shipping telemetry connection screen, with a fictional TLS endpoint, named ingest tokens, and connected agent." } }, } } satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Connected: Story = { play: async ({ canvas }) => {
  await expect(await canvas.findByRole("heading", { name: "Connect telemetry" })).toBeVisible();
  await expect(await canvas.findByText("Operations assistant")).toBeVisible();
} };

export const CreatedToken: Story = { play: async ({ canvas, userEvent }) => {
  await userEvent.type(await canvas.findByLabelText("Token name"), "edge-collector");
  await userEvent.click(canvas.getByRole("button", { name: "Create token" }));
  await waitFor(() => expect(canvas.getByText("Save this token now")).toBeVisible());
  await expect(canvas.getAllByText(new RegExp(`INGEST_TOKEN=${ingestToken}`))[0]).toBeVisible();
} };

export const RevokeToken: Story = { play: async ({ canvas, userEvent }) => {
  const revoke = await canvas.findAllByRole("button", { name: "Revoke" });
  await userEvent.click(revoke[0]);
  const dialog = within(await within(document.body).findByRole("dialog", { name: "Revoke default?" }));
  await waitFor(() => expect(dialog.getByText("Any Collector using this token is rejected immediately. Other tokens keep working. This cannot be undone.")).toBeVisible());
  await userEvent.click(dialog.getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(document.querySelector(".mantine-Modal-content")).not.toBeInTheDocument());
} };

export const IdentityProviderRoles: Story = { parameters: { scenario: { accountOwnership: "identity_provider_roles" } }, play: async ({ canvas }) => {
  await expect(await canvas.findByText("Managed by your identity provider")).toBeVisible();
} };

export const IdentityProviderRolesNoProvisioning: Story = { parameters: { scenario: { accountOwnership: "identity_provider_roles_no_provisioning" } }, play: async ({ canvas }) => {
  await expect(await canvas.findByText("New people cannot be added")).toBeVisible();
} };

export const IdentityProviderDirectory: Story = { parameters: { scenario: { accountOwnership: "identity_provider_directory" } }, play: async ({ canvas }) => {
  await expect(await canvas.findByText("Managed by your identity provider")).toBeVisible();
} };

export const ChatDisabled: Story = { parameters: { scenario: { chatDisabled: true } }, play: async ({ canvas }) => {
  await expect(await canvas.findByRole("heading", { name: "Chat is disabled" })).toBeVisible();
} };
