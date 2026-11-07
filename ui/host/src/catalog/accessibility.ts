import { docs } from "../../../links";

// Existing shipping markup; keep these exceptions local to affected stories.
// AppShell.Main and the dashboard/settings page both render <main>. Changing
// product landmarks is outside the catalog adoption; retain all other checks.
export const nestedMainRules = [
  { id: "landmark-main-is-top-level", enabled: false },
  { id: "landmark-no-duplicate-main", enabled: false },
  { id: "landmark-unique", enabled: false },
];

// PanelCard's labelled Mantine Loader renders a span without an explicit role.
export const loadingRules = [{ id: "aria-prohibited-attr", selector: '*:not(.mantine-Loader-root[aria-label="Loading panel"])' }];

// Settings uses an inherit-color documentation link with underline only on
// hover; its dark-scheme text does not reach 3:1 against surrounding prose.
export const settingsLinkRules = [{ id: "link-in-text-block", selector: `*:not(a[href="${docs.ingest}"]):not(a[href="${docs.connect}"])` }];

// Shipping alert rules have an unnamed actions column; limit this to its blank header.
export const alertRules = [{ id: "empty-table-header", selector: "*:not([data-alerts] th:empty)" }];

// Shipping log details put aria-expanded on table rows rather than treegrid rows.
export const logRules = [{ id: "aria-conditional-attr", selector: '*:not([data-search-page="logs"] tr[data-row-expanded])' }];
