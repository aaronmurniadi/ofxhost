/**
 * The plugin chain.
 *
 * The list is the processing order: the top node runs first. Reordering uses the
 * arrow buttons rather than drag and drop, because the chain is short and a button
 * is reachable from the keyboard.
 *
 * Nothing here holds a copy of the chain: every edit is a call to the Go side and a
 * re-read of its answer, so there is one source of truth.
 */

import { useEffect, useState } from "react";
import { chainApi, type ChainDTO, type PluginDTO } from "../api/document";

interface Props {
  /** Bumped by the shell when a render should be redone. */
  onChange: () => void;
}

export default function PluginsPanel({ onChange }: Props) {
  const [chain, setChain] = useState<ChainDTO | null>(null);
  const [plugins, setPlugins] = useState<PluginDTO[]>([]);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [filter, setFilter] = useState("");
  const [error, setError] = useState("");

  const reload = async () => {
    const [current, available] = await Promise.all([chainApi.current(), chainApi.plugins()]);
    setChain(current);
    setPlugins(available ?? []);
  };

  useEffect(() => {
    void reload();
  }, []);

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
      setPickerOpen(false);
      setFilter("");
      return result.error;
    });

  const visible = plugins.filter((plugin) => {
    if (filter === "") {
      return true;
    }
    const needle = filter.toLowerCase();
    return (
      plugin.label.toLowerCase().includes(needle) ||
      plugin.author.toLowerCase().includes(needle) ||
      plugin.identifier.toLowerCase().includes(needle)
    );
  });

  return (
    <section className="panel panel--plugins" aria-label="Plugins">
      <h2 className="panel__title">Plugins</h2>

      <div className="plugins__actions">
        <button className="button" onClick={() => setPickerOpen((open) => !open)}>
          Add plugin
        </button>
      </div>

      {pickerOpen ? (
        <div className="plugins__picker">
          <input
            className="input"
            autoFocus
            placeholder="Search…"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          />
          <ul className="plugins__list">
            {visible.length === 0 ? (
              <li className="panel__placeholder">No matches</li>
            ) : (
              visible.map((plugin) => (
                <li key={plugin.identifier}>
                  <button className="plugins__choice" onClick={() => void add(plugin.index)}>
                    <span>{plugin.label}</span>
                    {plugin.author ? <span className="plugins__author">{plugin.author}</span> : null}
                  </button>
                </li>
              ))
            )}
          </ul>
        </div>
      ) : null}

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
              <button
                className="icon"
                title={node.enabled ? "Disable" : "Enable"}
                onClick={() => void run(() => chainApi.setEnabled(node.index, !node.enabled))}
              >
                {node.enabled ? "On" : "Off"}
              </button>
              <button
                className="icon"
                title="Move up"
                disabled={node.index === 0}
                onClick={() => void run(() => chainApi.move(node.index, node.index - 1))}
              >
                ↑
              </button>
              <button
                className="icon"
                title="Move down"
                disabled={node.index === (chain?.nodes?.length ?? 1) - 1}
                onClick={() => void run(() => chainApi.move(node.index, node.index + 1))}
              >
                ↓
              </button>
              <button
                className="icon"
                title="Remove"
                onClick={() => void run(() => chainApi.remove(node.index))}
              >
                ✕
              </button>
            </li>
          ))
        )}
      </ul>
    </section>
  );
}
