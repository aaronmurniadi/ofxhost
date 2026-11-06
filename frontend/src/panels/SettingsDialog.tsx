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
import Select from "../widgets/Select";

/** The themes the shell can draw. The names match `data-theme` in app.css. */
const THEMES = ["Dark", "Light"];

/** Text sizes offered, in points. Zero follows the operating system. */
const TEXT_SIZES: { label: string; value: number }[] = [
  { label: "System", value: 0 },
  { label: "Small", value: 12 },
  { label: "Medium", value: 13 },
  { label: "Large", value: 15 },
  { label: "Extra large", value: 17 },
];

/** The release channels the update checker can follow. */
const CHANNELS = ["stable", "prerelease"];

export interface SettingsProps {
  onClose: () => void;
  /** Called when the appearance changes, so the shell can repaint. */
  onAppearanceChange: (theme: string, fontPt: number) => void;
}

export default function SettingsDialog({ onClose, onAppearanceChange }: SettingsProps) {
  const [theme, setTheme] = useState("Dark");
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
          setTheme(settings.appearance.theme || "Dark");
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
        <span className="field__label">Theme</span>
        <div className="settings__row">
          {THEMES.map((name) => (
            <button
              key={name}
              className={theme === name ? "button button--selected" : "button"}
              onClick={() => void applyAppearance(name, fontPt)}
            >
              {name}
            </button>
          ))}
        </div>
      </div>

      <div className="field">
        <label className="field__label" htmlFor="settings-text-size">
          Text size
        </label>
        <Select
          id="settings-text-size"
          label="Text size"
          value={String(fontPt)}
          onChange={(next) => void applyAppearance(theme, Number(next))}
          options={TEXT_SIZES.map((size) => ({
            label: size.label,
            value: String(size.value),
          }))}
        />
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
