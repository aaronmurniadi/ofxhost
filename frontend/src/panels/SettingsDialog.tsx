/**
 * The settings dialog.
 *
 * What it changes is applied immediately and saved on the spot, so there is no Apply
 * button and no way to leave with an unsaved change. Each control writes through the
 * session service, which persists before it reports success; a failed save leaves the
 * previous value in place rather than showing one the file does not contain.
 */

import { useEffect, useState } from "react";
import { sessionApi } from "../api/session";
import Modal from "../widgets/Modal";
import ParamNumber from "../widgets/ParamNumber";
import Select from "../widgets/Select";
import { DEFAULT_THEME, normaliseTheme, THEMES } from "./themes";

/**
 * The text size bounds, in points.
 *
 * These are the same numbers as `project.MinFontPt` and `project.MaxFontPt`, which are
 * where the value is enforced. They are repeated here so the control cannot offer a size
 * the service would replace with the system default - a box that accepts 4 and then shows
 * 0 is a control that lies.
 */
const MIN_FONT_PT = 6;
const MAX_FONT_PT = 36;

/** The default text size, in points. Zero means follow the operating system. */
const SYSTEM_FONT_PT = 0;

/** The release channels the update checker can follow. */
const CHANNELS = ["stable", "prerelease"];

export interface SettingsProps {
  onClose: () => void;
  /** Called when the appearance changes, so the shell can repaint. */
  onAppearanceChange: (theme: string, fontPt: number) => void;
}

export default function SettingsDialog({ onClose, onAppearanceChange }: SettingsProps) {
  const [theme, setTheme] = useState(DEFAULT_THEME);
  const [fontPt, setFontPt] = useState(0);
  const [channel, setChannel] = useState("stable");
  const [update, setUpdate] = useState("");
  const [error, setError] = useState("");

  // Read the stored values once, so the dialog opens showing the truth rather than
  // the defaults.
  useEffect(() => {
    void (async () => {
      try {
        const settings = await sessionApi.settings();
        if (settings?.appearance) {
          setTheme(normaliseTheme(settings.appearance.theme));
          setFontPt(settings.appearance.fontPt ?? 0);
        }
      } catch {
        // Keep the defaults; the dialog still works.
      }
    })();
  }, []);

  const applyAppearance = async (nextTheme: string, nextFontPt: number) => {
    setTheme(nextTheme);
    setFontPt(nextFontPt);
    try {
      await sessionApi.setTheme(nextTheme, nextFontPt);
      onAppearanceChange(nextTheme, nextFontPt);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "The setting could not be saved.");
    }
  };

  const checkForUpdate = async () => {
    setUpdate("Checking…");
    try {
      const release = await sessionApi.checkForUpdate();
      if (release?.tag) {
        setUpdate(`Version ${release.tag} is available.`);
      } else {
        // The checker is not wired in every build, so this is a statement about the
        // answer rather than about the build.
        setUpdate("No newer version was reported.");
      }
    } catch (err) {
      setUpdate(err instanceof Error ? err.message : "The check failed.");
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title="Settings"
      actions={
        <button className="button button--primary" onClick={onClose}>
          Done
        </button>
      }
    >
      {error !== "" ? <p className="plugins__error">{error}</p> : null}

      <div className="field">
        <label className="field__label" htmlFor="settings-theme">
          Theme
        </label>
        {/* A dropdown, not a row of buttons. Fourteen themes as buttons filled the dialog
            with two lines of chrome and said nothing about which was which; the dropdown
            also gives the list a scroll context and a keyboard model. */}
        <Select
          id="settings-theme"
          label="Theme"
          value={theme}
          onChange={(next) => void applyAppearance(next, fontPt)}
          options={THEMES.map((option) => ({ label: option.label, value: option.value }))}
        />
      </div>

      <div className="field">
        {/* The label names the number field through aria-labelledby: the field's input has
            no id of its own, so a htmlFor here would point at nothing. */}
        <span className="field__label" id="settings-text-size-label">
          Text size, in points
        </span>
        <div className="settings__row">
          <ParamNumber
            value={fontPt === SYSTEM_FONT_PT ? 10 : fontPt}
            min={MIN_FONT_PT}
            max={MAX_FONT_PT}
            integer
            step={1}
            disabled={false}
            label="Text size in points"
            labelId="settings-text-size-label"
            onCommit={(next) => void applyAppearance(theme, next)}
          />
          <span className="field__value">pt</span>
          <button
            className={fontPt === SYSTEM_FONT_PT ? "button button--selected" : "button"}
            onClick={() => void applyAppearance(theme, SYSTEM_FONT_PT)}
          >
            System
          </button>
        </div>
        <p className="field__hint">
          {MIN_FONT_PT} to {MAX_FONT_PT} points. System follows the operating system size.
        </p>
      </div>

      <div className="field">
        <label className="field__label" htmlFor="settings-channel">
          Update channel
        </label>
        <Select
          id="settings-channel"
          label="Update channel"
          value={channel}
          onChange={setChannel}
          options={CHANNELS.map((name) => ({ label: name, value: name }))}
        />
        <div className="settings__row">
          <button className="button" onClick={() => void checkForUpdate()}>
            Check for updates
          </button>
          {update !== "" ? <span className="field__value">{update}</span> : null}
        </div>
      </div>

      <p className="modal__note">
        The theme and text size apply at once and are kept for the next start.
      </p>
    </Modal>
  );
}
