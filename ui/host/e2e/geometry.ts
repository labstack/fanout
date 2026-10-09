export type Rect = { x: number; y: number; width: number; height: number };
export function overlaps(a: Rect, b: Rect, tolerance = 1): boolean {
  return Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x) > tolerance
    && Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y) > tolerance;
}
export function inside(child: Rect, parent: Rect, tolerance = 1): boolean {
  return child.x >= parent.x - tolerance && child.y >= parent.y - tolerance
    && child.x + child.width <= parent.x + parent.width + tolerance
    && child.y + child.height <= parent.y + parent.height + tolerance;
}
