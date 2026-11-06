import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import Select from "./Select";

/**
 * The trigger must show the chosen option's name, not its value.
 *
 * This is the regression for a real bug: without `items` on the root, Base UI has no
 * label to read while the popup is closed, because the items are rendered in a portal
 * that is not mounted. It falls back to the raw value, so every dropdown displayed "0".
 *
 * Rendering to a string needs no DOM, so the check runs in the ordinary test task.
 */
describe("Select", () => {
  it("shows the label of the chosen value", () => {
    const html = renderToStaticMarkup(
      <Select
        label="Preview resolution"
        value="2"
        onChange={() => {}}
        options={[
          { label: "Fit to preview", value: "0" },
          { label: "720p", value: "1" },
          { label: "1080p", value: "2" },
        ]}
      />,
    );

    expect(html).toContain("1080p");
    // The raw value must not be what the user reads.
    expect(html).not.toContain(">2<");
  });

  it("shows a long list's label, the case native selects handled worst", () => {
    const options = Array.from({ length: 47 }, (_, index) => ({
      label: `Preset ${index + 1}`,
      value: String(index),
    }));

    const html = renderToStaticMarkup(
      <Select label="Preset" value="46" onChange={() => {}} options={options} />,
    );
    expect(html).toContain("Preset 47");
  });
});
