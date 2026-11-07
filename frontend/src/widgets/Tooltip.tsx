/**
 * A tooltip, on Base UI.
 *
 * It replaces the `title` attribute, which waits about a second before appearing, cannot
 * be styled, and is invisible to a keyboard user entirely.
 *
 * The obvious implementation — wrap the child in a `<span>` and make that the trigger — is
 * wrong, and Base UI's composition guide is what exposed it. `Tooltip.Trigger` binds its
 * own focus handler, so the trigger must *be* the focusable element. A span cannot receive
 * focus, so wrapping produced a tooltip that appeared on hover and never on Tab: the exact
 * defect this component exists to remove.
 *
 * So the trigger is built by merging Base UI's props onto the caller's element through the
 * `render` prop, which is the documented way to say "this part is my element".
 *
 * Disabled children are the one exception. A disabled button receives neither hover nor
 * focus, so no arrangement of a trigger can make it explain itself; a wrapper span at least
 * restores hover. Nothing is lost by that, because a disabled control cannot take focus
 * anyway. Pass `aria-disabled` instead of `disabled` if the control should stay reachable
 * and simply refuse the action — then it is focusable and this component behaves normally.
 */

import { Tooltip } from "@base-ui/react/tooltip";
import { Children, isValidElement, type ReactElement, type ReactNode } from "react";

export interface TooltipProps {
  /** The text shown. An empty string renders the children with no tooltip at all. */
  text: string;
  /** Exactly one element: the control the tooltip describes. */
  children: ReactNode;
  /** Where it appears. Defaults to above. */
  side?: "top" | "bottom" | "left" | "right";
}

export default function TooltipHint({ text, children, side = "top" }: TooltipProps) {
  if (text === "") {
    return <>{children}</>;
  }

  const child = Children.only(children);
  if (!isValidElement(child)) {
    return <>{children}</>;
  }

  const disabled = (child.props as { disabled?: boolean }).disabled === true;

  const popup = (
    <Tooltip.Portal>
      <Tooltip.Positioner className="tooltip-positioner" side={side} sideOffset={6}>
        <Tooltip.Popup className="tooltip">{text}</Tooltip.Popup>
      </Tooltip.Positioner>
    </Tooltip.Portal>
  );

  if (disabled) {
    // The control cannot be interacted with, so the trigger has to be something that can.
    return (
      <Tooltip.Root>
        <Tooltip.Trigger render={<span className="tooltip-trigger" />}>{children}</Tooltip.Trigger>
        {popup}
      </Tooltip.Root>
    );
  }

  return (
    <Tooltip.Root>
      <Tooltip.Trigger render={child as ReactElement} />
      {popup}
    </Tooltip.Root>
  );
}
