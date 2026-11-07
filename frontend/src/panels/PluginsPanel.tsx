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

import TooltipHint from "../widgets/Tooltip";
import { Button } from "@cloudflare/kumo/components/button";
import { Input } from "@cloudflare/kumo/components/input";
import { LayerCard } from "@cloudflare/kumo/components/layer-card";
import { useEffect, useState } from "react";
import { chainApi, type ChainDTO, type PluginDTO } from "../api/document";

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
}

export default function PluginsPanel({ onChange, hasDocument }: Props) {
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
    <LayerCard render={<section aria-label="Plugins" />} className="panel panel--plugins">
      <h2 className="panel__title">Plugins</h2>

      <div className="plugins__actions">
        <TooltipHint text={hasDocument ? "Add a plugin to the chain" : "Open an image first"}>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setPickerOpen((open) => !open)}
            disabled={!hasDocument}
          >
            Add plugin
          </Button>
        </TooltipHint>
      </div>

      {pickerOpen && hasDocument ? (
        <div className="plugins__picker">
          <Input
            autoFocus
            aria-label="Search plugins"
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
                  <Button
                    variant="ghost"
                    size="sm"
                    className="plugins__choice"
                    onClick={() => void add(plugin.index)}
                  >
                    <span>{plugin.label}</span>
                    {plugin.author ? <span className="plugins__author">{plugin.author}</span> : null}
                  </Button>
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
              <Button
                variant="ghost"
                size="sm"
                className="plugins__name"
                onClick={() => void run(() => chainApi.select(node.index))}
              >
                {node.label}
              </Button>
              <TooltipHint text={node.enabled ? "Disable this plugin" : "Enable this plugin"}>
                <Button
                  variant="ghost"
                  size="xs"
                  shape="square"
                  aria-label={node.enabled ? "Disable this plugin" : "Enable this plugin"}
                  onClick={() => void run(() => chainApi.setEnabled(node.index, !node.enabled))}
                >
                  {node.enabled ? "On" : "Off"}
                </Button>
              </TooltipHint>
              <TooltipHint text="Move up">
                <Button
                  variant="ghost"
                  size="xs"
                  shape="square"
                  aria-label="Move up"
                  disabled={node.index === 0}
                  onClick={() => void run(() => chainApi.move(node.index, node.index - 1))}
                >
                  ↑
                </Button>
              </TooltipHint>
              <TooltipHint text="Move down">
                <Button
                  variant="ghost"
                  size="xs"
                  shape="square"
                  aria-label="Move down"
                  disabled={node.index === (chain?.nodes?.length ?? 1) - 1}
                  onClick={() => void run(() => chainApi.move(node.index, node.index + 1))}
                >
                  ↓
                </Button>
              </TooltipHint>
              <TooltipHint text="Remove from the chain">
                <Button
                  variant="ghost"
                  size="xs"
                  shape="square"
                  aria-label="Remove from the chain"
                  onClick={() => void run(() => chainApi.remove(node.index))}
                >
                  ✕
                </Button>
              </TooltipHint>
            </li>
          ))
        )}
      </ul>
    </LayerCard>
  );
}
