import { describe, expect, it } from "vitest";
import css from "../styles/app.css?raw";
import { DEFAULT_THEME, normaliseTheme, THEMES } from "./themes";

/**
 * What these protect: the list in this module and the blocks in the stylesheet have to agree.
 *
 * They are two files that a person edits by hand, and a mismatch is silent - the settings
 * panel offers a theme that draws the default colours, and nothing says so. Reading
 * `app.css` from the test is cheap and turns that into a failure.
 */
describe("themes", () => {
  it("has a stylesheet block for every theme it offers", () => {
    for (const theme of THEMES) {
      expect(css).toContain(`[data-theme="${theme.value}"]`);
    }
  });

  it("gives every theme a distinct value and label", () => {
    const values = new Set(THEMES.map((theme) => theme.value));
    const labels = new Set(THEMES.map((theme) => theme.label));
    expect(values.size).toBe(THEMES.length);
    expect(labels.size).toBe(THEMES.length);
  });

  it("offers the default theme first", () => {
    expect(THEMES[0].value).toBe(DEFAULT_THEME);
  });
});

describe("normaliseTheme", () => {
  it("lower cases a name a settings file stored with a capital", () => {
    // Earlier builds stored "Dark" and "Light".
    expect(normaliseTheme("Dark")).toBe("dark");
    expect(normaliseTheme("Light")).toBe("light");
  });

  it("drops the spaces of a display name", () => {
    expect(normaliseTheme("Forest Green")).toBe("forestgreen");
    expect(normaliseTheme("Paper and Ink")).toBe("paperandink");
  });

  it("falls back to the default for nothing at all", () => {
    expect(normaliseTheme("")).toBe(DEFAULT_THEME);
    expect(normaliseTheme("   ")).toBe(DEFAULT_THEME);
  });

  it("falls back to the default for a theme this build does not have", () => {
    // A theme removed in a later version must not leave the window unstyled.
    expect(normaliseTheme("Solarized")).toBe(DEFAULT_THEME);
  });
});
