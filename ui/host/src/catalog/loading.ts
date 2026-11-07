import { expect } from "storybook/test";

export async function assertLoadingState(label: HTMLElement, text: string, shape?: string) {
  const status = label.closest('[role="status"]');
  await expect(status).toBeInTheDocument();
  await expect(status).toHaveTextContent(text);
  const style = getComputedStyle(label);
  await expect(style.position).toBe("absolute");
  await expect(style.width).toBe("1px");
  await expect(style.height).toBe("1px");
  await expect(style.overflow).toBe("hidden");
  const placeholder = status!.querySelector('[aria-hidden="true"][inert]');
  await expect(placeholder).toBeVisible();
  if (shape) await expect(placeholder!.querySelector(`[data-skeleton-shape="${shape}"]`)).toBeVisible();
  return placeholder!;
}
