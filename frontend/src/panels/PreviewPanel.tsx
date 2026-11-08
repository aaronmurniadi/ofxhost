/**
 * The preview panel.
 *
 * The image arrives as an <img> whose src is a URL from the Go frame server, so
 * the pixels never travel through the JSON bridge. Pan and zoom are CSS transforms
 * on that element: no pixel work happens in JavaScript, which is what keeps a
 * large frame cheap to display.
 *
 * The Go side has already converted the frame to sRGB (decision D2), so the browser
 * applies only its normal sRGB-to-display transform. The preview therefore looks the
 * same in WebKit, WebView2 and WebKitGTK.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { anchoredZoom, clampPan } from "./previewView";
import { setZoomPercent } from "../widgets/zoomStore";

/** The zoom range, matching what the previous host allowed. */
const MIN_ZOOM = 0.05;
const MAX_ZOOM = 64;

/** How much one notch of the wheel zooms. */
const ZOOM_STEP = 1.1;

/** The distance one notch of a mouse wheel reports in `deltaY`. */
const WHEEL_NOTCH = 100;

/**
 * How many steps one frame may apply.
 *
 * A trackpad can report a whole gesture within a few frames. Without a cap, one frame would
 * take the zoom across its whole range and the user would see the destination but not the
 * travel.
 */
const MAX_STEPS_PER_FRAME = 3;

export interface PreviewState {
  url: string;
  width: number;
  height: number;
}

interface Props {
  preview: PreviewState | null;
  fileName: string;
  busy: boolean;
  /** rendering reports that the chain is being run for a new preview. */
  rendering: boolean;
  /** Called with the preview area, so "fit" can follow the panel size. */
  onResize: (width: number, height: number) => void;
  /** Asks for a fresh preview after the render size changed. */
  onChange: () => void;
}

export default function PreviewPanel({ preview, fileName, busy, rendering, onResize, onChange }: Props) {
  const canvasRef = useRef<HTMLDivElement | null>(null);
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  // canvasSize is the area the image has to fit into.
  //
  // Without it the image is drawn at its own width and height, so a 1920-wide preview
  // inside a 900-wide panel was simply clipped. Zoom is a multiple of the fit, which
  // makes "1" mean "the whole frame is visible" at every panel size.
  const [canvasSize, setCanvasSize] = useState({ width: 0, height: 0 });
  // drag holds where a pan started. It is a ref rather than state because it changes on
  // every pointer move and nothing renders from it.
  const drag = useRef<{ x: number; y: number; panX: number; panY: number } | null>(null);
  const [dragging, setDragging] = useState(false);

  // A new image resets the view, so the user sees the whole frame rather than a
  // corner of the previous one.
  //
  // It keys on the file and not on the preview URL: the URL changes every time a
  // parameter is edited, and resetting there would throw away the zoom on every
  // keystroke of a drag.
  useEffect(() => {
    setZoom(1);
    setPan({ x: 0, y: 0 });
  }, [fileName]);

  // Report the panel size on every resize. The observer is what makes "fit to
  // preview" follow a dragged panel edge without a resize handler on the window.
  useEffect(() => {
    const element = canvasRef.current;
    if (!element) {
      return;
    }
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const box = entry.contentRect;
        // The exact size is what the geometry uses. A panel edge rarely lands on a whole
        // pixel, and rounding up grants half a pixel of slack that does not exist, which
        // is exactly enough to let the image edge sit inside the canvas and show a hairline
        // of backdrop. The rounded size is still what the rest of the window is told.
        setCanvasSize({ width: box.width, height: box.height });
        onResize(Math.round(box.width), Math.round(box.height));
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [onResize]);

  // fitScale is the factor that puts the whole frame inside the canvas. Zoom
  // multiplies it, so wheel and trackpad change the view without ever losing the
  // frame on the first draw.
  const fitScale =
    preview && preview.width > 0 && preview.height > 0 && canvasSize.width > 0 && canvasSize.height > 0
      ? Math.min(canvasSize.width / preview.width, canvasSize.height / preview.height)
      : 1;

  // The zoom percentage is shown in the status line, one line of chrome rather than an
  // overlay that covers the picture. It goes into its own small store, not up through the
  // shell: a wheel changes it many times a second, and a prop would re-render every panel
  // on every step.
  //
  // One hundred percent means the whole frame is visible, which is what a double-click
  // restores. It is not the picture's pixel ratio: a frame rendered to fit a 900-pixel panel
  // reports 35% by that measure, so a double-click that had in fact reset the view looked as
  // though it had done nothing. The ratio is also the wrong question here - the render
  // follows the panel, so the "real size" changes with the window.
  useEffect(() => {
    setZoomPercent(Math.round(zoom * 100));
  }, [zoom]);

  /**
   * clampPan keeps the image from being dragged out of the panel.
   *
   * An image smaller than the canvas has no slack, so it stays centred. A larger one may
   * move until its edge meets the panel's, which is the furthest a user can meaningfully
   * look. Without this a single fast drag loses the picture with no way back except a
   * double-click, which is not discoverable.
   */
  const limitPan = useCallback(
    (next: { x: number; y: number }, scale: number) => {
      if (!preview) {
        return { x: 0, y: 0 };
      }
      return clampPan(
        next,
        scale,
        { width: preview.width, height: preview.height },
        { width: canvasSize.width, height: canvasSize.height },
      );
    },
    [preview, canvasSize.width, canvasSize.height],
  );

  // The mirrors the frame callback reads.
  //
  // The wheel handler runs outside React's event system, so it cannot close over the values
  // of the render it was created in: a coalesced update has to see the zoom the user
  // already reached. A ref holds the current value and the callback stays the same function
  // for the life of the panel.
  const zoomRef = useRef(zoom);
  zoomRef.current = zoom;
  const panRef = useRef(pan);
  panRef.current = pan;
  const fitRef = useRef(fitScale);
  fitRef.current = fitScale;
  const limitRef = useRef(limitPan);
  limitRef.current = limitPan;

  // The wheel accumulates between frames. A trackpad reports a zoom gesture as tens of
  // events a second, and applying each one meant a scale change - and so a re-raster of the
  // picture at its new size - per event. One update per frame is the most the screen can
  // show, and it is a fraction of the work.
  const wheelState = useRef({ delta: 0, x: 0, y: 0, frame: 0 });

  /** applyWheel turns everything a frame's worth of wheel events said into one step. */
  const applyWheel = useCallback(() => {
    const state = wheelState.current;
    state.frame = 0;
    const delta = state.delta;
    state.delta = 0;
    if (delta === 0) {
      return;
    }

    // A mouse notch is one step; a flung trackpad may earn two or three, so fast gestures
    // still cover ground without one frame jumping the whole range.
    const steps = Math.min(MAX_STEPS_PER_FRAME, Math.max(1, Math.round(Math.abs(delta) / WHEEL_NOTCH)));
    const factor = delta < 0 ? Math.pow(ZOOM_STEP, steps) : Math.pow(1 / ZOOM_STEP, steps);
    const current = zoomRef.current;
    const next = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, current * factor));
    if (next === current) {
      return;
    }

    // Zoom about the pointer, not the centre.
    //
    // A plain scale keeps the middle still, so whatever the user is looking at drifts
    // away as they zoom in and has to be chased with a pan. Anchoring means the pixel
    // under the cursor stays under the cursor, which is what makes zoom-then-pan a
    // single gesture instead of two.
    const anchored = anchoredZoom(panRef.current, { x: state.x, y: state.y }, next / current);

    setZoom(next);
    setPan(limitRef.current(anchored, fitRef.current * next));
  }, []);

  // The wheel listener is added by hand.
  //
  // React registers `wheel` as a passive listener, so the `preventDefault` in an onWheel
  // handler does nothing: the WebView is free to scroll an ancestor while the picture
  // zooms. `{ passive: false }` is what makes the call count.
  useEffect(() => {
    const element = canvasRef.current;
    if (element === null) {
      return;
    }
    const onWheel = (event: WheelEvent) => {
      event.preventDefault();
      const rect = element.getBoundingClientRect();
      const state = wheelState.current;
      state.delta += event.deltaY;
      // The anchor is the last position seen in the frame, which is where the user is
      // pointing now.
      state.x = event.clientX - rect.left - rect.width / 2;
      state.y = event.clientY - rect.top - rect.height / 2;
      if (state.frame !== 0) {
        return;
      }
      state.frame = requestAnimationFrame(applyWheel);
    };
    element.addEventListener("wheel", onWheel, { passive: false });
    return () => {
      element.removeEventListener("wheel", onWheel);
      if (wheelState.current.frame !== 0) {
        cancelAnimationFrame(wheelState.current.frame);
        wheelState.current.frame = 0;
      }
    };
  }, [applyWheel]);

  // A panel that grows while the image is panned leaves the pan outside the new limits, so
  // the image would sit against an edge with slack behind it. Re-clamping on every change
  // of scale or canvas size keeps the view consistent without asking the user to nudge it.
  useEffect(() => {
    setPan((current) => {
      const next = limitPan(current, fitScale * zoom);
      if (next.x === current.x && next.y === current.y) {
        return current;
      }
      return next;
    });
  }, [limitPan, fitScale, zoom]);

  const onPointerDown = useCallback(
    (event: React.PointerEvent<HTMLDivElement>) => {
      // Only the left button pans, and only when there is an image.
      if (event.button !== 0 || !preview) {
        return;
      }
      // Pointer capture is what lets a drag continue past the edge of the panel, which
      // is what a user does when the image is larger than the view.
      event.currentTarget.setPointerCapture(event.pointerId);
      drag.current = { x: event.clientX, y: event.clientY, panX: pan.x, panY: pan.y };
      setDragging(true);
    },
    [preview, pan],
  );

  const onPointerMove = useCallback(
    (event: React.PointerEvent<HTMLDivElement>) => {
      const started = drag.current;
      if (started === null) {
        return;
      }
      const next = {
        x: started.panX + (event.clientX - started.x),
        y: started.panY + (event.clientY - started.y),
      };
      setPan(limitPan(next, fitScale * zoom));
    },
    [limitPan, fitScale, zoom],
  );

  const endDrag = useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    if (drag.current === null) {
      return;
    }
    drag.current = null;
    setDragging(false);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  }, []);

  const onDoubleClick = useCallback(() => {
    setZoom(1);
    setPan({ x: 0, y: 0 });
  }, []);

  return (
    <section className="panel panel--preview" aria-label="Preview">
      <div
        ref={canvasRef}
        className={dragging ? "preview__canvas preview__canvas--dragging" : "preview__canvas"}
        onDoubleClick={onDoubleClick}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
      >
        {busy ? (
          <p className="panel__placeholder">Opening…</p>
        ) : preview ? (
          <img
            className="preview__image"
            src={preview.url}
            width={preview.width}
            height={preview.height}
            alt={fileName || "Preview"}
            draggable={false}
            style={{
              transform: `translate(${pan.x}px, ${pan.y}px) scale(${fitScale * zoom})`,
            }}
          />
        ) : (
          <p className="panel__placeholder">Open a RAW file to see the preview.</p>
        )}

        {/* The wait is shown where the user is looking, not only in the status line: a render
            of a full chain can take a second, and an untouched picture with no mark on it
            reads as a control that did nothing.
            The overlay takes no pointer, so panning and zooming keep working while a render
            runs. */}
        {rendering ? (
          <div className="preview__busy" role="status" aria-label="Rendering">
            <span className="preview__spinner" aria-hidden />
          </div>
        ) : null}
      </div>
    </section>
  );
}
