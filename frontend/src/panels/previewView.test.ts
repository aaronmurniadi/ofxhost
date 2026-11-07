import { describe, expect, it } from "vitest";
import { anchoredZoom, clampPan } from "./previewView";

const image = { width: 1000, height: 500 };
const canvas = { width: 500, height: 500 };

describe("clampPan", () => {
  it("keeps a small image centred", () => {
    // At scale 0.4 the image is 400x200, smaller than the canvas on both axes, so there is
    // nothing to pan and a drag must not move it.
    expect(clampPan({ x: 120, y: 120 }, 0.4, image, canvas)).toEqual({ x: 0, y: 0 });
  });

  it("allows movement only to the edge of the image", () => {
    // At scale 2 the image is 2000 wide against a 500 canvas: half the overhang is 750.
    const moved = clampPan({ x: 5000, y: 0 }, 2, image, canvas);
    expect(moved.x).toBe(750);
    expect(clampPan({ x: -5000, y: 0 }, 2, image, canvas).x).toBe(-750);
  });

  it("clamps each axis by its own overhang", () => {
    // 2000x1000 at scale 2 against 500x500: limits are 750 across and 250 down.
    const moved = clampPan({ x: 5000, y: 5000 }, 2, { width: 1000, height: 500 }, canvas);
    expect(moved).toEqual({ x: 750, y: 250 });
  });

  it("does not move an image that fits exactly", () => {
    expect(clampPan({ x: 10, y: 10 }, 1, { width: 500, height: 500 }, canvas)).toEqual({ x: 0, y: 0 });
  });

  it("survives a preview that has no size yet", () => {
    expect(clampPan({ x: 5, y: 5 }, 1, { width: 0, height: 0 }, canvas)).toEqual({ x: 0, y: 0 });
  });
});

describe("anchoredZoom", () => {
  it("keeps the point under the cursor still when zooming in", () => {
    // The point under the cursor, before and after, must be the same image point.
    const pan = { x: 0, y: 0 };
    const offset = { x: 100, y: 0 };
    const ratio = 2;
    const next = anchoredZoom(pan, offset, ratio);

    // Re-derive where the cursor's image point lands under both pans and compare.
    const before = (offset.x - pan.x) / 1;
    const after = (offset.x - next.x) / ratio;
    expect(after).toBeCloseTo(before);
  });

  it("scales the pan when the pointer is at the centre", () => {
    // Zooming about the centre of the canvas stretches the image centre away from it by
    // the same ratio; nothing stays put except the very centre point of the canvas.
    expect(anchoredZoom({ x: 30, y: -20 }, { x: 0, y: 0 }, 1.5)).toEqual({ x: 45, y: -30 });
  });

  it("is the identity when the ratio is one", () => {
    expect(anchoredZoom({ x: 12, y: 8 }, { x: 90, y: 40 }, 1)).toEqual({ x: 12, y: 8 });
  });
});
