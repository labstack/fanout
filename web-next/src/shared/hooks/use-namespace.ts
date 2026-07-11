import { useSearchParams } from "react-router";

/** Namespace from the URL; empty string means the default namespace. */
export function useNamespace(): string {
  const [params] = useSearchParams();
  return params.get("namespace") ?? "";
}
