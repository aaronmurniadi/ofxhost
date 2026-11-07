/**
 * The view maths of the preview panel.
 *
 * Kept apart from the component because it is the part with rules rather than markup: how
 * far the image may be dragged, and where the pointer lands after a zoom. Both are pure
 * functions of numbers, so they are tested directly instead of through a rendered panel
 * that no test here can see.
 */

/** Point is a pan offset in screen pixels from the centre of the canvas. */
export interface Point {
  x: number;
  y: number;
}

/** Size is a width and height in pixels. */
export interface Size {
  width: number;
  height: number;
}

/**
 * clampPan keeps the image from being dragged out of the panel.
 *
 * An image smaller than the canvas has no slack, so it stays centred. A larger one may move
 * until its edge meets the panel's, which is the furthest a user can meaningfully look.
 * Without the limit a single fast drag loses the picture, and the only way back is a
 * double-click that nothing advertises.
 *
 * `scale` is the total scale applied to the image: the fit factor times the zoom.
 */
export function clampPan(pan: Point, scale: number, image: Size, canvas: Size): Point {
  if (image.width <= 0 || image.height <= 0 || scale <= 0) {
    return { x: 0, y: 0 };
  }
  const shownWidth = image.width * scale;
  const shownHeight = image.height * scale;
  // Half the overhang, because the pan is measured from the centre of the canvas.
  const limitX = Math.max(0, (shownWidth - canvas.width) / 2);
  const limitY = Math.max(0, (shownHeight - canvas.height) / 2);
  return {
    x: Math.min(limitX, Math.max(-limitX, pan.x)),
    y: Math.min(limitY, Math.max(-limitY, pan.y)),
  };
}

/**
 * anchoredZoom returns the pan that keeps the point under the cursor still.
 *
 * A plain scale about the centre makes whatever the user is looking at drift away as they
 * zoom in, and it has to be chased with a pan afterwards. Anchoring turns zoom and pan into
 * one gesture.
 *
 * `offset` is the cursor position relative to the centre of the canvas, in screen pixels.
 * `ratio` is the new zoom divided by the old one.
 */
export function anchoredZoom(pan: Point, offset: Point, ratio: number): Point {
  return {
    x: offset.x - ratio * (offset.x - pan.x),
    y: offset.y - ratio * (offset.y - pan.y),
  };
}
