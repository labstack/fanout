export const TRACE_PAGE_SIZE = 8;

export function pageItems<T>(items: T[], page = 1, pageSize = TRACE_PAGE_SIZE): T[] {
  // Waterfall rows keep the server's span order, including on the first page.
  const start = (page - 1) * pageSize;
  return items.slice(start, start + pageSize);
}
