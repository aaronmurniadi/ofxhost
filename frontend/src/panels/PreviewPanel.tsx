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

import { LayerCard } from "@cloudflare/kumo/components/layer-card";
import { useCallback, useEffect, useRef, useState } from "react";
import { anchoredZoom, clampPan } from "./previewView";

/** The zoom range, matching what the previous host allowed. */
const MIN_ZOOM = 0.05;
const MAX_ZOOM = 64;

export interface PreviewState {
  url: string;
  width: number;
  height: number;
}

interface Props {
  preview: PreviewState | null;
  fileName: string;
  busy: boolean;
  /** Called with the preview area, so "fit" can follow the panel size. */
  onResize: (width: number, height: number) => void;
  /** Asks for a fresh preview after the render size changed. */
  onChange: () => void;
  /** Reports the zoom percentage, so the status bar can show it. */
  onZoomChange: (percent: number) => void;
}

export default function PreviewPanel({ preview, fileName, busy, onResize, onChange, onZoomChange }: Props) {
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
        const width = Math.round(box.width);
        const height = Math.round(box.height);
        setCanvasSize({ width, height });
        onResize(width, height);
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

  // The zoom percentage is shown in the status bar, one line of chrome rather than an
  // overlay that covers the picture.
  useEffect(() => {
    onZoomChange(Math.round(fitScale * zoom * 100));
  }, [onZoomChange, fitScale, zoom]);

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

  const onWheel = useCallback(
    (event: React.WheelEvent<HTMLDivElement>) => {
      // The page must not scroll behind the preview.
      event.preventDefault();

      const current = zoom;
      const factor = event.deltaY < 0 ? 1.1 : 1 / 1.1;
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
      const rect = event.currentTarget.getBoundingClientRect();
      const offsetX = event.clientX - rect.left - rect.width / 2;
      const offsetY = event.clientY - rect.top - rect.height / 2;
      const anchored = anchoredZoom(pan, { x: offsetX, y: offsetY }, next / current);

      setZoom(next);
      setPan(limitPan(anchored, fitScale * next));
    },
    [zoom, pan, fitScale, limitPan],
  );

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
    <LayerCard render={<section aria-label="Preview" />} className="panel panel--preview">
      <div
        ref={canvasRef}
        className={dragging ? "preview__canvas preview__canvas--dragging" : "preview__canvas"}
        onWheel={onWheel}
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

      </div>
    </LayerCard>
  );
}
