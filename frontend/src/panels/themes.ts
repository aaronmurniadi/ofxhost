/**
 * The themes the shell can draw.
 *
 * `value` is the selector the stylesheet uses - `[data-theme="forestgreen"]` - and the label
 * is what the settings panel shows. They are kept apart because a theme name is a stored
 * setting and a display name is text: "Paper and Ink" is easier to read with its spaces, and
 * a selector with spaces is one more thing to quote correctly in CSS and in a test.
 *
 * Dark and Light are this project's own. The rest are styles posted in the Dear ImGui theme
 * thread: Dark (rounded) and the eleven palettes after it come from
 * https://github.com/ocornut/imgui/issues/707#issuecomment-4107169777, and Slate Blue from
 * https://github.com/ocornut/imgui/issues/707#issuecomment-4121907946. All of them are
 * translated in `app.css`.
 */

export interface ThemeOption {
  /** The text the settings panel shows. */
  label: string;
  /** The `data-theme` value, which is also what the settings file stores. */
  value: string;
}

/** DEFAULT_THEME is what a fresh install draws. */
export const DEFAULT_THEME = "dark";

/** THEMES is the list, in the order the settings panel offers it. */
export const THEMES: ThemeOption[] = [
  { label: "Dark", value: "dark" },
  { label: "Dark (rounded)", value: "darkrounded" },
  { label: "Light", value: "light" },
  { label: "Forest Green", value: "forestgreen" },
  { label: "Amethyst", value: "amethyst" },
  { label: "Sapphire", value: "sapphire" },
  { label: "Amber Yellow", value: "amberyellow" },
  { label: "Dracula", value: "dracula" },
  { label: "Catppuccin Mocha", value: "catppuccinmocha" },
  { label: "Gruvbox Hard", value: "gruvboxhard" },
  { label: "Crimson Vesuvius", value: "crimsonvesuvius" },
  { label: "Rose Quartz", value: "rosequartz" },
  { label: "Cyberpunk", value: "cyberpunk" },
  { label: "Slate Blue", value: "slateblue" },
  { label: "Paper and Ink", value: "paperandink" },
];

/**
 * normaliseTheme turns a stored theme name into the selector value.
 *
 * A settings file written by an earlier build holds "Dark" and "Light", with a capital and no
 * other difference. Lower case is what the stylesheet matches, so it is what a stored name is
 * reduced to; anything that is not a known theme still reaches the stylesheet, where the
 * `:root` tokens apply and the window keeps working in the default theme.
 */
export function normaliseTheme(name: string): string {
  const lowered = name.trim().toLowerCase();
  if (lowered === "") {
    return DEFAULT_THEME;
  }
  const squashed = lowered.replace(/[^a-z0-9]/g, "");
  for (const theme of THEMES) {
    if (theme.value === squashed) {
      return theme.value;
    }
  }
  return DEFAULT_THEME;
}
