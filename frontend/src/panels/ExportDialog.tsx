/**
 * The export dialog.
 *
 * Every control is driven by what the chosen format can actually do, which is why the
 * Go side publishes a capability row per format rather than a fixed form: a format
 * that cannot carry transparency hides the alpha setting, and one that cannot embed a
 * profile warns before the user picks a wide-gamut space.
 *
 * The dialog never guesses what will be written. The depth list comes from the format,
 * and the Go side clamps again before encoding, so a stale dialog cannot produce a file
 * the format cannot honour — it produces a note instead.
 */

import Modal from "../widgets/Modal";
import { useEffect, useState } from "react";
import { exportApi, type ExportOptionsDTO, type ExportResult, type FormatDTO } from "../api/document";

interface Props {
  onClose: () => void;
  onExported: (result: ExportResult) => void;
}

export default function ExportDialog({ onClose, onExported }: Props) {
  const [options, setOptions] = useState<ExportOptionsDTO | null>(null);
  const [format, setFormat] = useState(0);
  const [space, setSpace] = useState(0);
  const [intent, setIntent] = useState(0);
  const [depth, setDepth] = useState(8);
  const [quality, setQuality] = useState(92);
  const [lossless, setLossless] = useState(false);
  // A sidecar records how the file was produced. It is opt-in: the export is the
  // deliverable, and an unrequested file beside it is clutter the user must clean up.
  const [writeSidecar, setWriteSidecar] = useState(false);
  const [error, setError] = useState("");
  const [notes, setNotes] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void (async () => {
      const loaded = await exportApi.options();
      setOptions(loaded);
      setFormat(loaded.defaultFormat);
      setSpace(loaded.defaultSpace);
      setIntent(loaded.defaultIntent);
      setDepth(loaded.defaultDepth);
      setQuality(loaded.defaultQuality);
    })();
  }, []);

  if (options === null) {
    return (
      <div className="modal" role="dialog" aria-modal="true" aria-label="Export">
        <div className="modal__body">
          <p className="panel__placeholder">Reading the export settings…</p>
        </div>
      </div>
    );
  }

  const formats = options.formats ?? [];
  const spaces = options.colorSpaces ?? [];
  const intents = options.intents ?? [];
  const current: FormatDTO | undefined = formats.find((f) => f.index === format);
  const depths = current?.bitDepths ?? [8];

  // Choosing a format resets the depth to one the format can write, so the dialog can
  // never show a combination the encoder would have to overrule.
  const chooseFormat = (next: FormatDTO) => {
    setFormat(next.index);
    if (!(next.bitDepths ?? []).includes(depth)) {
      const list = next.bitDepths ?? [8];
      setDepth(list[list.length - 1]);
    }
    if (!next.lossless) {
      setLossless(false);
    }
  };

  const wideGamut = space !== 0; // sRGB is index 0, per the domain's order
  const profileWarning = wideGamut && current !== undefined && !current.carriesICC;

  const run = async () => {
    setBusy(true);
    setError("");
    setNotes([]);

    const suggested = `${options.defaultName || "export"}${current?.extension ?? ".png"}`;
    const path = await exportApi.save(suggested);
    if (path === "") {
      setBusy(false);
      return;
    }

    const result = await exportApi.run({
      format,
      space,
      intent,
      depth,
      quality,
      lossless,
      writeSidecar,
      path,
    });
    setBusy(false);

    if (!result.ok) {
      setError(result.error);
      return;
    }
    setNotes(result.notes ?? []);
    onExported(result);
  };

  return (
    <Modal
      open
      onClose={onClose}
      title="Export"
      wide
      actions={
        <>
          <button className="button button--primary" onClick={() => void run()} disabled={busy}>
            {busy ? "Exporting…" : "Export"}
          </button>
          <button className="button" onClick={onClose} disabled={busy}>
            Close
          </button>
        </>
      }
    >

        <div className="field">
          <label className="field__label" htmlFor="export-format">
            Format
          </label>
          <select
            id="export-format"
            className="input"
            value={format}
            onChange={(event) => {
              const next = formats.find((f) => f.index === Number(event.target.value));
              if (next !== undefined) {
                chooseFormat(next);
              }
            }}
          >
            {formats.map((f) => (
              <option key={f.index} value={f.index}>
                {f.name}
              </option>
            ))}
          </select>
        </div>

        <div className="field">
          <label className="field__label" htmlFor="export-space">
            Colour space
          </label>
          <select
            id="export-space"
            className="input"
            value={space}
            onChange={(event) => setSpace(Number(event.target.value))}
          >
            {spaces.map((name, index) => (
              <option key={name} value={index}>
                {name}
                {name === options.workingSpace ? " (working space)" : ""}
              </option>
            ))}
          </select>
        </div>

        <div className="field">
          <label className="field__label" htmlFor="export-intent">
            Rendering intent
          </label>
          <select
            id="export-intent"
            className="input"
            value={intent}
            onChange={(event) => setIntent(Number(event.target.value))}
          >
            {intents.map((name, index) => (
              <option key={name} value={index}>
                {name}
              </option>
            ))}
          </select>
        </div>

        <div className="field">
          <label className="field__label" htmlFor="export-depth">
            Bit depth
          </label>
          <select
            id="export-depth"
            className="input"
            value={depth}
            onChange={(event) => setDepth(Number(event.target.value))}
          >
            {depths.map((d) => (
              <option key={d} value={d}>
                {d}-bit
              </option>
            ))}
          </select>
        </div>

        <label className="field field--inline">
          <input
            type="checkbox"
            checked={writeSidecar}
            onChange={(event) => setWriteSidecar(event.target.checked)}
          />
          <span title="Records the plugin chain and colour spaces beside the file">
            Write sidecar with the chain
          </span>
        </label>

        {current?.lossless ? (
          <label className="field field--inline">
            <input
              type="checkbox"
              checked={lossless}
              onChange={(event) => setLossless(event.target.checked)}
            />
            <span>Lossless</span>
          </label>
        ) : null}

        {current?.quality && !lossless ? (
          <div className="field">
            <label className="field__label" htmlFor="export-quality">
              Quality
            </label>
            <div className="field__row">
              <input
                id="export-quality"
                className="input params__slider"
                type="range"
                min={1}
                max={100}
                value={quality}
                onChange={(event) => setQuality(Number(event.target.value))}
              />
              <span className="field__value">{quality}</span>
            </div>
          </div>
        ) : null}

        {profileWarning ? (
          <p className="plugins__error">
            {current?.name} cannot store a colour profile, so a {spaces[space]} file would be
            ambiguous. Choose sRGB, or a format that carries a profile.
          </p>
        ) : null}

        {current !== undefined && !current.carriesAlpha ? (
          <p className="modal__note">Transparency is dropped: {current.name} cannot store it.</p>
        ) : null}

        <p className="modal__note">
          The export runs the whole chain at full resolution and converts into the chosen
          space before writing.
        </p>

        {error ? <p className="plugins__error">{error}</p> : null}
        {notes.map((note) => (
          <p key={note} className="modal__note">
            {note}
          </p>
        ))}

    </Modal>
  );
}
