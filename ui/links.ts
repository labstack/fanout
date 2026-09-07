/* Outbound destinations, in one place because they are the only URLs the
 * product hard-codes and the documentation site is not served from this
 * repository. Change them here.
 *
 * There is deliberately no link to the source repository or the vendor site in
 * the application. Both sat in the account menu — where someone goes to act on
 * their own account — and neither answers a question an operator has while
 * using the product. They belong on the marketing site. */
export const docs = {
  home: "https://fanout.run/docs",
  /** Sending telemetry: endpoint, authentication, collector and SDK setup. */
  ingest: "https://fanout.run/docs/ingest",
} as const;
