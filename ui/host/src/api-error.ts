export type Problem = { path: string; message: string; hint?: string };

export class ApiError extends Error {
  constructor(message: string, readonly status: number, readonly problems: Problem[] = []) {
    super(message);
  }
}
