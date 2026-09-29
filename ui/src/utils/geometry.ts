// Page geometry of builder fields. Every box is a fraction 0..1 of its page
// with the origin at the TOP-LEFT corner, so it survives any render scale.
// Everything here keeps a box fully inside the page (the server refuses
// x + w > 1 or y + h > 1) and rounds to 4 decimals like auto-detect does.
import type { FieldType } from '@/api/types'

export interface Box { x: number; y: number; w: number; h: number }

/** Smallest box a field may shrink to (about 12 × 8 pt on A4). */
export const MIN_W = 0.02
export const MIN_H = 0.01

export function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v))
}

export function round4(v: number): number {
  return Math.round(v * 10000) / 10000
}

/** Bounds a box to the page: size first (min..1), then the position. */
export function clampBox(b: Box): Box {
  const w = round4(clamp(b.w, MIN_W, 1))
  const h = round4(clamp(b.h, MIN_H, 1))
  return { x: clamp(round4(b.x), 0, round4(1 - w)), y: clamp(round4(b.y), 0, round4(1 - h)), w, h }
}

/** Moves a box by a page fraction; it stops at the page edges keeping its size. */
export function moveBox(b: Box, dx: number, dy: number): Box {
  return clampBox({ ...b, x: b.x + dx, y: b.y + dy })
}

/** Grows or shrinks a box from its bottom-right corner, never past the page. */
export function resizeBox(b: Box, dw: number, dh: number): Box {
  const w = round4(clamp(b.w + dw, MIN_W, 1 - b.x))
  const h = round4(clamp(b.h + dh, MIN_H, 1 - b.y))
  return { x: b.x, y: b.y, w, h }
}

/** Default size of a new field per type (a signature is larger than a checkbox). */
export const DEFAULT_SIZE: Record<FieldType, { w: number; h: number }> = {
  text: { w: 0.25, h: 0.03 },
  number: { w: 0.12, h: 0.03 },
  signature: { w: 0.25, h: 0.07 },
  initials: { w: 0.1, h: 0.05 },
  date: { w: 0.15, h: 0.03 },
  checkbox: { w: 0.03, h: 0.022 },
  select: { w: 0.2, h: 0.03 },
  radio: { w: 0.2, h: 0.06 },
  image: { w: 0.2, h: 0.12 },
  file: { w: 0.2, h: 0.03 },
  cells: { w: 0.3, h: 0.03 },
  stamp: { w: 0.18, h: 0.1 },
}

/** A default-sized box for the type centred on the point (fractions), inside the page. */
export function boxAt(type: FieldType, fx: number, fy: number): Box {
  const { w, h } = DEFAULT_SIZE[type]
  return clampBox({ x: fx - w / 2, y: fy - h / 2, w, h })
}

/** Intersection over union of two boxes (0 = apart, 1 = identical). */
export function overlap(a: Box, b: Box): number {
  const iw = Math.max(0, Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x))
  const ih = Math.max(0, Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y))
  const inter = iw * ih
  const union = a.w * a.h + b.w * b.h - inter
  return union > 0 ? inter / union : 0
}

/** Two boxes on the same page cover the same spot (auto-detect de-duplication). */
export function sameSpot(a: Box & { page: number }, b: Box & { page: number }): boolean {
  return a.page === b.page && overlap(a, b) >= 0.5
}
