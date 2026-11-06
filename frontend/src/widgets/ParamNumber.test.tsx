import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import ParamNumber, { ParamScrubLabel } from "./ParamNumber";

const base = {
  value: 1,
  min: 0,
  max: 4,
  integer: false,
  step: 0.01,
  disabled: false,
  label: "gain",
  onCommit: () => {},
};

describe("ParamNumber", () => {
  it("gives the input an accessible name", () => {
    const html = renderToStaticMarkup(<ParamNumber {...base} />);
    expect(html).toContain('aria-label="gain"');
  });

  it("lets a visible label name the input instead", () => {
    // Two names on one control is worse than none: aria-labelledby wins, so aria-label
    // must be dropped when a label element exists.
    const html = renderToStaticMarkup(<ParamNumber {...base} labelId="param-gain-label" />);
    expect(html).toContain('aria-labelledby="param-gain-label"');
    expect(html).not.toContain('aria-label="gain"');
  });

  it("renders the draggable name as a real label with the shared id", () => {
    // The cursor itself appears only while a drag is live, so a static render cannot show
    // it; what can be checked is that the name is a <label> the input can point at.
    const html = renderToStaticMarkup(
      <ParamScrubLabel {...base} labelId="param-gain-label">
        Gain
      </ParamScrubLabel>,
    );
    expect(html).toContain("<label");
    expect(html).toContain('id="param-gain-label"');
    expect(html).toContain("Gain");
    // ScrubArea is the element that carries the drag gesture.
    expect(html).toContain("params__label--scrub");
  });
});
