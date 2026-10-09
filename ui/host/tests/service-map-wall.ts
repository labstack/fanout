import type { Frame } from "../../panels/types";

// The acceptance wall: checkout/recommendation fan-out and two isolated services.
const names = ["load-generator", "frontend-proxy", "frontend", "checkout", "recommendation", "product-catalog", "product-reviews", "flagd", "cart", "currency", "ad", "payment", "fraud-detection", "shipping", "quote", "email", "image-provider", "kafka"];
const routes = [[0,1],[1,2],[2,3],[2,4],[2,5],[2,6],[2,8],[2,9],[2,10],[3,5],[3,8],[3,9],[3,11],[3,13],[3,15],[4,5],[4,6],[4,7],[6,7],[11,12],[13,14],[0,5],[3,7]];
const columns = ["kind", "service", "caller", "callee", "health", "calls", "spans"];
const rows = [...names.map(name => ["node", name, "", "", "healthy", null, 100]), ...routes.map(([a,b]) => ["edge", "", names[a], names[b], "", 10, null])];
export const wallFrame: Frame = { rows: rows.length, columns: columns.map(name => ({ name, type: ["calls", "spans"].includes(name) ? "number" : "string", role: ["calls", "spans"].includes(name) ? "measure" : "dimension" })), values: columns.map((_, i) => rows.map(row => row[i])) };
