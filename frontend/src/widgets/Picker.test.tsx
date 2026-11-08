import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import Picker, { matches, sortByLabel, type PickerItem } from "./Picker";

const items: PickerItem[] = [
  { value: "0", label: "Bloom", author: "Acme", identifier: "com.acme.bloom" },
  { value: "1", label: "Denoise", author: "OfxHost", identifier: "org.ofxhost.denoise" },
  { value: "2", label: "sharpen", author: "Acme", identifier: "com.acme.sharpen" },
  { value: "3", label: "Vintage", author: "", identifier: "com.nobody.vintage" },
];

/**
 * These render to a string and call the helpers directly, so they need no DOM.
 *
 * What they protect: the trigger must name what it adds, and the search must cover the
 * author and the identifier as well as the label - a user looking for a plugin often knows
 * those instead of the display name, and a search that ignores them reads as "not
 * installed".
 */
describe("Picker", () => {
  it("renders the trigger with its label and no open popup", () => {
    const html = renderToStaticMarkup(<Picker items={items} label="Add plugin" onSelect={() => {}} />);
    expect(html).toContain("Add plugin");
    // The list lives in a portal, which is not mounted while the popup is closed.
    expect(html).not.toContain("Denoise");
  });
});

describe("matches", () => {
  it("keeps every item for an empty query", () => {
    expect(items.filter((item) => matches(item, ""))).toHaveLength(4);
    expect(items.filter((item) => matches(item, "   "))).toHaveLength(4);
  });

  it("matches the label regardless of case", () => {
    expect(items.filter((item) => matches(item, "deno"))).toHaveLength(1);
  });

  it("matches the author and the identifier", () => {
    expect(items.filter((item) => matches(item, "acme"))).toHaveLength(2);
    expect(items.filter((item) => matches(item, "org.ofxhost"))).toHaveLength(1);
  });

  it("keeps nothing when nothing matches", () => {
    expect(items.filter((item) => matches(item, "kaleidoscope"))).toHaveLength(0);
  });
});

describe("sortByLabel", () => {
  it("sorts by the name the user reads, ignoring case", () => {
    // "sharpen" is lower case in the fixture: a plugin author types what they like, and a
    // plain sort would file it after every capitalised name.
    expect(sortByLabel(items).map((item) => item.label)).toEqual([
      "Bloom",
      "Denoise",
      "sharpen",
      "Vintage",
    ]);
  });

  it("leaves the caller's list alone", () => {
    const original = items.map((item) => item.label);
    sortByLabel(items);
    expect(items.map((item) => item.label)).toEqual(original);
  });

  it("keeps every item", () => {
    expect(sortByLabel(items)).toHaveLength(items.length);
  });
});
