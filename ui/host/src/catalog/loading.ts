import { visuallyHidden } from "../../.storybook/checks";
import { expect } from "storybook/test";

export async function assertLoadingState(label: HTMLElement, text: string, shape?: string) {
  const status = label.closest('[role="status"]');
  await expect(status).toBeInTheDocument();
  await expect(status).toHaveTextContent(text);
  await expect(visuallyHidden(label, status as HTMLElement)).toBe(true);
  const placeholder = status!.querySelector('[aria-hidden="true"][inert]');
  await expect(placeholder).toBeVisible();
  if (shape) await expect(placeholder!.querySelector(`[data-skeleton-shape="${shape}"]`)).toBeVisible();
  return placeholder!;
}
