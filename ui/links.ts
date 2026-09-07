/* Outbound destinations, in one place because they are the only URLs the
 * product hard-codes. The pages live in this repository under site/, published
 * to https://fanout.run by Cloudflare Pages, and Starlight serves that content
 * collection from the site root — so the paths carry no /docs prefix.
 *
 * There is deliberately no link to the source repository or the vendor site in
 * the application. Both sat in the account menu — where someone goes to act on
 * their own account — and neither answers a question an operator has while
 * using the product. They belong on the marketing site. */
export const docs = {
  /** Where an operator starts reading, not the marketing page above it. */
  home: "https://fanout.run/start/what-fanout-is",
  /** Sending telemetry: endpoint, authentication, collector and SDK setup. */
  ingest: "https://fanout.run/start/send-telemetry",
} as const;
