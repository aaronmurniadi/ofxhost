/**
 * The bridge to the settings service.
 *
 * The layout lives on the Go side, not in the frontend. A panel size that only
 * existed in JavaScript would be lost on reload, and it would disagree with the
 * settings file the View menu also writes to.
 *
 * Regenerate the bindings after changing a binding method:
 *
 *     wails3 generate bindings -d frontend/bindings ./cmd/ofxhost
 */

import { Call } from "@wailsio/runtime";

const SERVICE = "github.com/aaronmurniadi/ofxhost/internal/transport/binds";

/**
 * PanelsDTO is the size of each resizable panel, as a percentage of its group.
 *
 * A zero means the panel is hidden. There is no separate "visible" flag: a panel
 * dragged shut and a panel hidden from the View menu are the same fact.
 */
export interface PanelsDTO {
  right: number;
  plugins: number;
  filmstrip: number;
}

/** ThemeDTO is the appearance the window draws with. */
export interface ThemeDTO {
  theme: string;
  fontPt: number;
}

/** SettingsDTO is what the window needs for its first draw. */
export interface SettingsDTO {
  panels: PanelsDTO;
  appearance: ThemeDTO;
  defaultSpace: string;
}

/** UpdateDTO is one published build. */
export interface UpdateDTO {
  tag: string;
  pageUrl: string;
  assetUrl: string;
}

async function call<T>(method: string, ...args: unknown[]): Promise<T> {
  return (await Call.ByName(`${SERVICE}.Session.${method}`, ...args)) as T;
}

/**
 * The layout the window starts with.
 *
 * A failure here must not stop the window from opening, so the caller falls back
 * to the defaults rather than blocking on the answer.
 */
export const DEFAULT_PANELS: PanelsDTO = { right: 22, plugins: 55, filmstrip: 16 };

export const sessionApi = {
  settings: (): Promise<SettingsDTO> => call("Settings"),

  setPanels: (panels: PanelsDTO): Promise<PanelsDTO> => call("SetPanels", panels),

  setTheme: (theme: string, fontPt: number): Promise<void> =>
    call("SetTheme", theme, fontPt),

  checkForUpdate: (): Promise<UpdateDTO> => call("CheckForUpdate"),
};
