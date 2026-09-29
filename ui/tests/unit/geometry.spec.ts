import { describe, expect, it } from 'vitest'
import { MIN_H, MIN_W, boxAt, clampBox, moveBox, overlap, resizeBox, sameSpot } from '@/utils/geometry'

const inside = (b: { x: number; y: number; w: number; h: number }) => b.x >= 0 && b.y >= 0 && b.x + b.w <= 1 + 1e-6 && b.y + b.h <= 1 + 1e-6

describe('field geometry (page fractions, top-left origin)', () => {
  it('a palette drop centres a default-sized box on the point', () => {
    expect(boxAt('signature', 0.5, 0.5)).toEqual({ x: 0.375, y: 0.465, w: 0.25, h: 0.07 })
    expect(boxAt('checkbox', 0.3, 0.4)).toEqual({ x: 0.285, y: 0.389, w: 0.03, h: 0.022 })
  })

  it('a drop near an edge or corner keeps the whole box on the page', () => {
    expect(boxAt('signature', 0.99, 0.99)).toEqual({ x: 0.75, y: 0.93, w: 0.25, h: 0.07 })
    expect(boxAt('text', 0, 0)).toEqual({ x: 0, y: 0, w: 0.25, h: 0.03 })
  })

  it('moving stops at the page edges and keeps the size', () => {
    const b = { x: 0.7, y: 0.9, w: 0.25, h: 0.05 }
    expect(moveBox(b, 0.2, 0.2)).toEqual({ x: 0.75, y: 0.95, w: 0.25, h: 0.05 })
    expect(moveBox(b, -2, -2)).toEqual({ x: 0, y: 0, w: 0.25, h: 0.05 })
    expect(moveBox(b, -0.1, 0.01)).toEqual({ x: 0.6, y: 0.91, w: 0.25, h: 0.05 })
  })

  it('resizing is bounded by the minimum size and the page edge', () => {
    const b = { x: 0.6, y: 0.8, w: 0.2, h: 0.1 }
    expect(resizeBox(b, 1, 1)).toEqual({ x: 0.6, y: 0.8, w: 0.4, h: 0.2 })
    expect(resizeBox(b, -1, -1)).toEqual({ x: 0.6, y: 0.8, w: MIN_W, h: MIN_H })
    expect(resizeBox(b, 0.05, -0.02)).toEqual({ x: 0.6, y: 0.8, w: 0.25, h: 0.08 })
  })

  it('clamping rounds to 4 decimals without ever crossing the page edge', () => {
    const b = clampBox({ x: 0.33335, y: 0.123456, w: 0.66665, h: 2 })
    expect(b.h).toBe(1)
    expect(b.y).toBe(0)
    expect(inside(b)).toBe(true)
    expect(String(b.w).split('.')[1]?.length ?? 0).toBeLessThanOrEqual(4)
    expect(inside(clampBox({ x: -1, y: 5, w: 0, h: 0 }))).toBe(true)
  })

  it('overlap is intersection over union; the same spot needs the same page', () => {
    const a = { x: 0.1, y: 0.1, w: 0.2, h: 0.1 }
    expect(overlap(a, a)).toBe(1)
    expect(overlap(a, { x: 0.5, y: 0.5, w: 0.1, h: 0.1 })).toBe(0)
    expect(overlap(a, { x: 0.2, y: 0.1, w: 0.2, h: 0.1 })).toBeCloseTo(1 / 3)
    expect(sameSpot({ ...a, page: 1 }, { ...a, x: 0.11, page: 1 })).toBe(true)
    expect(sameSpot({ ...a, page: 1 }, { ...a, page: 2 })).toBe(false)
  })
})
