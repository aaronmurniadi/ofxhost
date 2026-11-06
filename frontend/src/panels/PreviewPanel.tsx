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
import { api } from "../api/document";

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
  zoomLabel: string;
  busy: boolean;
  /** Called with the preview area, so "fit" can follow the panel size. */
  onResize: (width: number, height: number) => void;
  /** Asks for a fresh preview after the render size changed. */
  onChange: () => void;
}

export default function PreviewPanel({ preview, fileName, zoomLabel, busy, onResize, onChange }: Props) {
  // The render size is a control on the panel because it is the one setting a user
  // reaches for while iterating: a smaller frame makes every parameter edit return
  // sooner. It is persisted by the Go side alongside the other view settings.
  const [resolutions, setResolutions] = useState<string[]>([]);
  const [resolution, setResolution] = useState(0);

  useEffect(() => {
    void (async () => {
      try {
        setResolutions((await api.previewResolutions()) ?? []);
      } catch {
        // The control stays hidden when the list cannot be read.
      }
    })();
  }, []);

  const chooseResolution = useCallback(
    async (index: number) => {
      setResolution(index);
      await api.setPreviewResolution(index);
      // Ask for the frame again at the new size; the old one is now the wrong one.
      onChange();
    },
    [onChange],
  );
  const canvasRef = useRef<HTMLDivElement | null>(null);
  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });

  // A new preview resets the view, so the user sees the whole frame rather than a
  // corner of the previous one.
  useEffect(() => {
    setZoom(1);
    setPan({ x: 0, y: 0 });
  }, [preview?.url]);

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
        onResize(Math.round(box.width), Math.round(box.height));
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [onResize]);

  const onWheel = useCallback((event: React.WheelEvent<HTMLDivElement>) => {
    // The page must not scroll behind the preview.
    event.preventDefault();
    setZoom((current) => {
      const factor = event.deltaY < 0 ? 1.1 : 1 / 1.1;
      const next = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, current * factor));
      return next;
    });
  }, []);

  const onDoubleClick = useCallback(() => {
    setZoom(1);
    setPan({ x: 0, y: 0 });
  }, []);

  return (
    <section className="panel panel--preview" aria-label="Preview">
      <div
        ref={canvasRef}
        className="preview__canvas"
        onWheel={onWheel}
        onDoubleClick={onDoubleClick}
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
              transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`,
            }}
          />
        ) : (
          <p className="panel__placeholder">Open a RAW file to see the preview.</p>
        )}

        {resolutions.length > 0 ? (
          <label className="preview__overlay preview__overlay--resolution">
            <span className="visually-hidden">Preview resolution</span>
            <select
              className="preview__resolution"
              value={resolution}
              onChange={(event) => void chooseResolution(Number(event.target.value))}
            >
              {resolutions.map((name, index) => (
                <option key={name} value={index}>
                  {name}
                </option>
              ))}
            </select>
          </label>
        ) : null}
        {fileName ? <span className="preview__overlay preview__overlay--name">{fileName}</span> : null}
        {preview ? <span className="preview__overlay preview__overlay--zoom">{zoomLabel}</span> : null}
      </div>
    </section>
  );
}
