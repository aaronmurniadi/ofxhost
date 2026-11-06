import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import TooltipHint from "./Tooltip";

/**
 * The trigger must be the control itself, not a wrapper around it.
 *
 * Base UI's Tooltip.Trigger binds its own focus handler, so a trigger that cannot receive
 * focus produces a tooltip that appears on hover and never on Tab. Wrapping the child in a
 * span did exactly that, which defeated the purpose of replacing the title attribute.
 */
describe("TooltipHint", () => {
  it("makes the control itself the trigger, so it can be focused", () => {
    const html = renderToStaticMarkup(
      <TooltipHint text="Move up">
        <button type="button" aria-label="Move up">
          ↑
        </button>
      </TooltipHint>,
    );

    // The element itself is the trigger: Base UI merged its props onto the caller's
    // button rather than wrapping it, so the markup begins with that button.
    expect(html.startsWith("<button")).toBe(true);
    expect(html).toContain('aria-label="Move up"');
    // The wrapper that broke keyboard access must be gone.
    expect(html).not.toContain('class="tooltip-trigger"');
    // And the caller's own children survive the merge.
    expect(html).toContain("↑");
  });

  it("still explains a disabled control, which cannot be focused anyway", () => {
    const html = renderToStaticMarkup(
      <TooltipHint text="Open an image first">
        <button type="button" disabled>
          Add plugin
        </button>
      </TooltipHint>,
    );

    // The control keeps its disabled attribute; the span is only there to catch hover.
    expect(html).toContain("disabled");
    expect(html).toContain("tooltip-trigger");
  });

  it("renders nothing extra when there is no hint", () => {
    const html = renderToStaticMarkup(
      <TooltipHint text="">
        <button type="button">Plain</button>
      </TooltipHint>,
    );
    expect(html).toBe('<button type="button">Plain</button>');
  });
});
