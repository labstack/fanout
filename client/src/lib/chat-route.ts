export function buildChatPath(prompt?: string, token?: string): string {
  const params = new URLSearchParams();
  if (token) params.set("token", token);
  if (prompt) params.set("q", prompt);
  const query = params.toString();
  return query ? `/chat?${query}` : "/chat";
}

export function buildDashboardPath(token?: string): string {
  if (!token) return "/";
  const params = new URLSearchParams({ token });
  return `/?${params.toString()}`;
}
