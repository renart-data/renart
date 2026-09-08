export type DropTargetBounds = {
  id: string;
  left: number;
  right: number;
  top: number;
  bottom: number;
};

// Screen-space distances remain usable when React Flow is zoomed or panned.
// Anchors must stay fixed while their visual/drop affordance expands.
export function nearestDropTarget(
  targets: DropTargetBounds[],
  x: number,
  y: number,
  radius = 56,
): string | null {
  let selected: string | null = null;
  let distance = radius * radius;
  let area = Infinity;
  for (const target of targets) {
    if (target.right <= target.left || target.bottom <= target.top) continue;
    const dx = Math.max(target.left - x, 0, x - target.right);
    const dy = Math.max(target.top - y, 0, y - target.bottom);
    const next = dx * dx + dy * dy;
    const nextArea = (target.right - target.left) * (target.bottom - target.top);
    if (next < distance || (next === distance && nextArea < area)) {
      selected = target.id;
      distance = next;
      area = nextArea;
    }
  }
  return selected;
}
