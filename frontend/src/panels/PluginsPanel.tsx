/**
 * The plugin chain, and the commands that act on the open file.
 *
 * There is no bar above the panels (D12). The actions that were there live here, at the
 * top of this panel, in the order the previous host uses: Open, Reopen last, Export,
 * Settings, then the preview size, then a status line, then the chain itself. One place
 * for the commands, and the whole window left for the picture.
 *
 * The list is the processing order: the top node runs first. Reordering uses the
 * arrow buttons rather than drag and drop, because the chain is short and a button
 * is reachable from the keyboard.
 *
 * Nothing here holds a copy of the chain: every edit is a call to the Go side and a
 * re-read of its answer, so there is one source of truth.
 */

import TooltipHint from "../widgets/Tooltip";
import { useEffect, useState } from "react";
import { chainApi, type ChainDTO, type PluginDTO } from "../api/document";
import Picker from "../widgets/Picker";
import Select from "../widgets/Select";
import ZoomReadout from "../widgets/ZoomReadout";

/**
 * Status is what the header of this panel reports.
 *
 * One object rather than ten props: every field describes the same thing - what is on
 * screen and whether the program is working - and they are read together, in one line.
 */
export interface PanelStatus {
  /** The open file's name, or the empty string when nothing is open. */
  fileName: string;
  /** The image's pixel dimensions, as the document reports them. */
  width: number;
  height: number;
  /** The space the chain works in, chosen when the file opened (D8). */
  space: string;
  /** The outcome of the last action. */
  message: string;
  /** The build version. */
  version: string;
}

interface Props {
  /** Bumped by the shell when a render should be redone. */
  onChange: () => void;
  /**
   * Whether an image is open.
   *
   * A chain belongs to a document, so there is nothing to add a plugin to until one is
   * open. The Go side refuses the call as well; this hides the control so the rule is
   * visible rather than only enforced.
   */
  hasDocument: boolean;
  /**
   * The path of the open document.
   *
   * Opening a file replaces the chain with the one recorded for it, so the list has to be
   * read again when this changes. Without it the panel kept the empty chain it read before
   * any file was open, and a restored chain was never drawn.
   */
  documentPath: string;
  /** Asks the user for a file, then for the space to develop it into. */
  onOpen: () => void;
  /** The file that was open last time, offered as a one-click restore. */
  lastImage: string;
  /** Opens the file offered above. */
  onReopenLast: () => void;
  /** Opens the export dialog. */
  onExport: () => void;
  /** Opens the settings dialog. */
  onOpenSettings: () => void;
  /** What the panel header reports. */
  status: PanelStatus;
  /** The preview render sizes, by name. Empty hides the control. */
  resolutions: string[];
  /** The chosen index into `resolutions`. */
  resolution: number;
  /** Changes the preview render size. */
  onChooseResolution: (index: number) => void;
  /** The colour space names, in enum order. Empty hides the control. */
  spaces: string[];
  /** The chosen index into `spaces`: the space a file is developed into. */
  inputSpace: number;
  /** Changes the space, which develops the open file again into it. */
  onChooseSpace: (index: number) => void;
}

export default function PluginsPanel({
  onChange,
  hasDocument,
  documentPath,
  onOpen,
  lastImage,
  onReopenLast,
  onExport,
  onOpenSettings,
  status,
  resolutions,
  resolution,
  onChooseResolution,
  spaces,
  inputSpace,
  onChooseSpace,
}: Props) {
  const [chain, setChain] = useState<ChainDTO | null>(null);
  const [plugins, setPlugins] = useState<PluginDTO[]>([]);
  const [error, setError] = useState("");

  const reload = async () => {
    const [current, available] = await Promise.all([chainApi.current(), chainApi.plugins()]);
    setChain(current);
    setPlugins(available ?? []);
  };

  useEffect(() => {
    void reload();
  }, [documentPath]);

  const run = async (action: () => Promise<string | unknown>) => {
    const result = await action();
    if (typeof result === "string" && result !== "") {
      setError(result);
    } else {
      setError("");
    }
    await reload();
    onChange();
  };

  const add = (pluginIndex: number) =>
    run(async () => {
      const result = await chainApi.add(pluginIndex);
      return result.error;
    });

  // The picker's items carry the author and the identifier as well as the label, so a search
  // can use any of the three.
  const choices = plugins.map((plugin) => ({
    value: String(plugin.index),
    label: plugin.label,
    author: plugin.author,
    identifier: plugin.identifier,
  }));

  return (
    <section className="panel panel--plugins" aria-label="Plugins">
      {/* The commands. Open and Reopen last are file actions, Export needs a document,
          and Settings belongs to the program rather than to the picture. */}
      <div className="plugins__actions">
        <button className="button button--primary" onClick={onOpen}>
          Open
        </button>
        {lastImage !== "" && !hasDocument ? (
          <TooltipHint text={lastImage}>
            <button className="button" onClick={onReopenLast}>
              Reopen last
            </button>
          </TooltipHint>
        ) : null}
        <TooltipHint text={hasDocument ? "Export the chain at full resolution" : "Open a file first"}>
          <button className="button" onClick={onExport} disabled={!hasDocument}>
            Export
          </button>
        </TooltipHint>
        <button className="button" onClick={onOpenSettings}>
          Settings
        </button>
      </div>

      {resolutions.length > 0 ? (
        <div className="plugins__control">
          <Select
            id="preview-resolution"
            label="Preview size"
            value={String(resolution)}
            onChange={(next) => onChooseResolution(Number(next))}
            options={resolutions.map((name, index) => ({ label: name, value: String(index) }))}
          />
        </div>
      ) : null}

      {/* The space every file is developed into. It sits beside the preview size because
          both are view settings rather than edits to the picture, and because the previous
          host keeps the colour space and the preview resolution in this same order in this
          same panel (D12).
          A change here decodes the open file again: the space is decided by the decode, so
          the pixels cannot be relabelled afterwards (PLAN.md §7.5). */}
      {spaces.length > 0 ? (
        <div className="plugins__control">
          <Select
            id="input-space"
            label="Input space"
            value={String(inputSpace)}
            onChange={(next) => onChooseSpace(Number(next))}
            options={spaces.map((name, index) => ({ label: name, value: String(index) }))}
          />
        </div>
      ) : null}

      {/* What is open, how it is being shown, and what just happened.
          The busy mark is not here: a render is shown at the centre of the picture, where the
          user is already looking, and a second one in a line of text said the same thing
          twice. */}
      <p className="status">
        <span className="status__name" title={documentPath}>
          {hasDocument ? status.fileName : "No image"}
        </span>
        {hasDocument ? (
          <>
            <span className="status__sep" aria-hidden>
              ·
            </span>
            <span className="status__value">
              {status.width}×{status.height}
            </span>
            <span className="status__sep" aria-hidden>
              ·
            </span>
            <span className="status__value">{status.space}</span>
            <span className="status__sep" aria-hidden>
              ·
            </span>
            {/* The zoom has its own source of truth: a wheel changes it many times a
                second, and routing that through this panel's props would re-render the
                parameter tree on each step. */}
            <ZoomReadout />
          </>
        ) : null}
        <span className="status__message" role="status" aria-live="polite">
          {status.message}
        </span>
        <span className="status__version">{status.version}</span>
      </p>

      <h2 className="panel__title">Chain</h2>

      <div className="plugins__actions">
        {/* A disabled picker cannot receive hover, so the hint that says why lives on a
            disabled button instead of on the dropdown itself. */}
        {hasDocument ? (
          <Picker
            items={choices}
            label="Add plugin"
            placeholder="Search a plugin…"
            onSelect={(value) => void add(Number(value))}
          />
        ) : (
          <TooltipHint text="Open an image first">
            <button className="button" disabled>
              Add plugin
            </button>
          </TooltipHint>
        )}
      </div>

      {error ? <p className="plugins__error">{error}</p> : null}
      {chain?.message ? <p className="panel__placeholder">{chain.message}</p> : null}

      <ul className="plugins__chain">
        {(chain?.nodes ?? []).length === 0 ? (
          <li className="panel__placeholder">Add a plugin to build a chain.</li>
        ) : (
          (chain?.nodes ?? []).map((node) => (
            <li
              key={`${node.index}-${node.identifier}`}
              className={node.selected ? "plugins__node plugins__node--selected" : "plugins__node"}
            >
              <button
                className="plugins__name"
                onClick={() => void run(() => chainApi.select(node.index))}
              >
                {node.label}
              </button>
              <TooltipHint text={node.enabled ? "Disable this plugin" : "Enable this plugin"}>
                <button
                  className="icon"
                  aria-label={node.enabled ? "Disable this plugin" : "Enable this plugin"}
                  onClick={() => void run(() => chainApi.setEnabled(node.index, !node.enabled))}
                >
                  {node.enabled ? "On" : "Off"}
                </button>
              </TooltipHint>
              <TooltipHint text="Move up">
                <button
                  className="icon"
                  aria-label="Move up"
                  disabled={node.index === 0}
                  onClick={() => void run(() => chainApi.move(node.index, node.index - 1))}
                >
                  ↑
                </button>
              </TooltipHint>
              <TooltipHint text="Move down">
                <button
                  className="icon"
                  aria-label="Move down"
                  disabled={node.index === (chain?.nodes?.length ?? 1) - 1}
                  onClick={() => void run(() => chainApi.move(node.index, node.index + 1))}
                >
                  ↓
                </button>
              </TooltipHint>
              <TooltipHint text="Remove from the chain">
                <button
                  className="icon"
                  aria-label="Remove from the chain"
                  onClick={() => void run(() => chainApi.remove(node.index))}
                >
                  ✕
                </button>
              </TooltipHint>
            </li>
          ))
        )}
      </ul>
    </section>
  );
}
