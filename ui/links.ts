/* Outbound destinations, in one place because they are the only URLs the
 * product hard-codes. The pages live in this repository under site/, published
 * to https://pitot.dev by Cloudflare Pages. Documentation lives under /docs;
 * the marketing landing page stays at the site root.
 *
 * There is deliberately no link to the source repository or the vendor site in
 * the application. Both sat in the account menu — where someone goes to act on
 * their own account — and neither answers a question an operator has while
 * using the product. They belong on the marketing site. */
export const docs = {
  /** Where an operator starts reading, not the marketing page above it. */
  home: "https://pitot.dev/docs",
  /** Sending telemetry: endpoint, authentication, collector and SDK setup. */
  ingest: "https://pitot.dev/docs/get-started/send-telemetry",
  connect: "https://pitot.dev/docs/guides/mcp",
  /** Who owns accounts and roles when an identity provider is configured. */
  authentication: "https://pitot.dev/docs/concepts/authentication",
} as const;
