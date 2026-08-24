import { defineCollection } from "astro:content";
// `z` re-exported from `astro:content` is deprecated in Astro 7; the bundled
// copy is the supported path and keeps a single zod version in the graph.
import { z } from "astro/zod";
import { docsLoader } from "@astrojs/starlight/loaders";
import { docsSchema } from "@astrojs/starlight/schema";

// Three fields exist for agents rather than readers.
//
// `summary` is the one-line description that appears beside the page in
// llms.txt. Without it the index is a list of titles, which tells an agent what
// a page is called but not whether it is worth fetching.
//
// `read_when` states the situations in which this page is the right one. It is
// retrieval metadata authored by the person who knows the answer, instead of
// inferred from the prose by whatever is doing the retrieving.
//
// `status` is the honesty marker, and its vocabulary is closed so a fourth
// value is a build error rather than a quiet new claim:
//
//   shipped      the binary on the current tag does this.
//   preview      it runs, and its shape may still change without a migration.
//                Fanout is pre-release and breaks things deliberately, so this
//                is a real state here rather than a hedge.
//   planned      designed and written down, but nothing executes it yet.
//
// A page that claims more than the binary delivers is the failure these exist
// to prevent. `generated` marks a page written by cmd/fanout-docgen, which the
// gate regenerates and compares — edit the generator, never the page.
export const collections = {
  docs: defineCollection({
    loader: docsLoader(),
    schema: docsSchema({
      extend: z.object({
        summary: z.string().max(240).optional(),
        read_when: z.array(z.string()).optional(),
        status: z.enum(["shipped", "preview", "planned"]).default("shipped"),
        generated: z.boolean().default(false),
      }),
    }),
  }),
};
