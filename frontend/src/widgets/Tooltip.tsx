/**
 * A tooltip, on Kumo.
 *
 * It replaces the `title` attribute, which waits about a second before appearing, cannot be
 * styled, and is invisible to a keyboard user entirely.
 *
 * The trigger must be the control itself. A tooltip that wraps its child in a span cannot
 * show on Tab, because the span is what the library listens to and a span cannot take focus
 * — which is the exact defect this component exists to remove. Kumo's `render` prop is the
 * documented way to say "the trigger is this element", so the caller's control is passed
 * through rather than wrapped.
 *
 * A disabled control receives neither hover nor focus, so no arrangement of a trigger can
 * make it explain itself; for that case the child is wrapped in a span, which at least
 * restores hover. Nothing is lost, because a disabled control cannot be focused anyway.
 * Pass `aria-disabled` instead of `disabled` to keep a control reachable and simply refuse
 * the action — then it behaves like any other and this exception does not apply.
 */

import { Tooltip } from "@cloudflare/kumo/components/tooltip";
import { Children, isValidElement, type ReactElement, type ReactNode } from "react";

export interface TooltipProps {
  /** The text shown. An empty string renders the children with no tooltip at all. */
  text: string;
  /** Exactly one element: the control the tooltip describes. */
  children: ReactNode;
}

export default function TooltipHint({ text, children }: TooltipProps) {
  if (text === "") {
    return <>{children}</>;
  }

  const child = Children.only(children);
  if (!isValidElement(child)) {
    return <>{children}</>;
  }

  const disabled = (child.props as { disabled?: boolean }).disabled === true;
  if (disabled) {
    // Kumo takes the trigger's own content as children when `render` names a bare
    // element, so the control is passed through rather than dropped.
    return (
      <Tooltip content={text} render={<span className="tooltip-trigger" />}>
        {children}
      </Tooltip>
    );
  }

  return <Tooltip content={text} render={child as ReactElement} />;
}
