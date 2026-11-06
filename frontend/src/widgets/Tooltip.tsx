/**
 * A tooltip, on Base UI.
 *
 * It replaces the `title` attribute, which waits about a second before appearing, cannot
 * be styled, and is invisible to a keyboard user entirely. Applied to a button, this
 * shows on focus as well as hover.
 *
 * The wrapper renders a span rather than cloning its child, so a disabled button still
 * receives hover events: a disabled element does not fire them, and "why is this
 * disabled" is the question a tooltip most needs to answer.
 */

import { Tooltip } from "@base-ui/react/tooltip";
import type { ReactNode } from "react";

export interface TooltipProps {
  /** The text shown. An empty string renders the children with no tooltip at all. */
  text: string;
  /** The element the tooltip describes. */
  children: ReactNode;
  /** Where it appears. Defaults to above. */
  side?: "top" | "bottom" | "left" | "right";
}

export default function TooltipHint({ text, children, side = "top" }: TooltipProps) {
  if (text === "") {
    return <>{children}</>;
  }

  return (
    <Tooltip.Root>
      <Tooltip.Trigger render={<span className="tooltip-trigger" />}>{children}</Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Positioner className="tooltip-positioner" side={side} sideOffset={6}>
          <Tooltip.Popup className="tooltip">{text}</Tooltip.Popup>
        </Tooltip.Positioner>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}
