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
import Select from "../widgets/Select";
import TooltipHint from "../widgets/Tooltip";
import { Button } from "@cloudflare/kumo/components/button";
import { Checkbox } from "@cloudflare/kumo/components/checkbox";
import { Slider } from "@cloudflare/kumo/components/slider";
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
          <Button variant="primary" size="sm" onClick={() => void run()} disabled={busy}>
            {busy ? "Exporting…" : "Export"}
          </Button>
          <Button variant="secondary" size="sm" onClick={onClose} disabled={busy}>
            Close
          </Button>
        </>
      }
    >

        <div className="field">
          <label className="field__label" htmlFor="export-format">
            Format
          </label>
          <Select
            id="export-format"
            label="Format"
            value={String(format)}
            onChange={(next) => {
              const chosen = formats.find((f) => f.index === Number(next));
              if (chosen !== undefined) {
                chooseFormat(chosen);
              }
            }}
            options={formats.map((f) => ({ label: f.name, value: String(f.index) }))}
          />
        </div>

        <div className="field">
          <label className="field__label" htmlFor="export-space">
            Colour space
          </label>
          <Select
            id="export-space"
            label="Colour space"
            value={String(space)}
            onChange={(next) => setSpace(Number(next))}
            options={spaces.map((name, index) => ({
              label: name === options?.workingSpace ? `${name} (working space)` : name,
              value: String(index),
            }))}
          />
        </div>

        <div className="field">
          <label className="field__label" htmlFor="export-intent">
            Rendering intent
          </label>
          <Select
            id="export-intent"
            label="Rendering intent"
            value={String(intent)}
            onChange={(next) => setIntent(Number(next))}
            options={intents.map((name, index) => ({ label: name, value: String(index) }))}
          />
        </div>

        <div className="field">
          <label className="field__label" htmlFor="export-depth">
            Bit depth
          </label>
          <Select
            id="export-depth"
            label="Bit depth"
            value={String(depth)}
            onChange={(next) => setDepth(Number(next))}
            options={depths.map((d) => ({ label: `${d}-bit`, value: String(d) }))}
          />
        </div>

        <div className="field field--inline">
          <Checkbox
            checked={writeSidecar}
            onCheckedChange={(next) => setWriteSidecar(next === true)}
            label={
              <TooltipHint text="Records the plugin chain and the colour spaces beside the file, so the edit can be reproduced">
                <span>Write sidecar with the chain</span>
              </TooltipHint>
            }
          />
        </div>

        {current?.lossless ? (
          <div className="field field--inline">
            <Checkbox
              checked={lossless}
              onCheckedChange={(next) => setLossless(next === true)}
              label="Lossless"
            />
          </div>
        ) : null}

        {current?.quality && !lossless ? (
          <div className="field">
            <label className="field__label" htmlFor="export-quality">
              Quality
            </label>
            <div className="field__row">
              <Slider
                id="export-quality"
                className="params__slider"
                min={1}
                max={100}
                value={quality}
                getAriaLabel={() => "Quality"}
                onValueChange={(next) => setQuality(Array.isArray(next) ? next[0] : next)}
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
