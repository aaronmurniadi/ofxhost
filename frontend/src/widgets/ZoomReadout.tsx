/**
 * The zoom percentage, as the status line shows it.
 *
 * It subscribes to its own store instead of taking a prop, so a zoom step re-renders this
 * span and not the window around it. See `zoomStore.ts` for why that matters.
 */

import { useSyncExternalStore } from "react";
import { getZoomPercent, subscribeZoomPercent } from "./zoomStore";

export default function ZoomReadout() {
  const percent = useSyncExternalStore(subscribeZoomPercent, getZoomPercent);
  return (
    <span className="status__value" aria-label={`Zoom ${percent}%`}>
      {percent}%
    </span>
  );
}
