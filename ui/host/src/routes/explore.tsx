import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { ExplorePage } from "../explore/page";
import { parseExploreSearch, toExploreParams } from "../explore/search";

export const Route = createFileRoute("/explore")({
  component: Explore,
  validateSearch: (raw: Record<string, unknown>) => toExploreParams(parseExploreSearch(raw)),
});

function Explore() {
  const search = parseExploreSearch(Route.useSearch() as Record<string, unknown>);
  const navigate = useNavigate();
  return <ExplorePage search={search} onSearch={(next, replace) => void navigate({ to: "/explore", search: toExploreParams(next), replace })} />;
}
