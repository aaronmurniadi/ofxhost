/**
 * The parameters of the selected node.
 *
 * Every control is driven by the kind the plugin declared, so a plugin the host has
 * never seen still gets the right control: a double gets a slider and a number box, a
 * choice gets a dropdown, a colour gets four numbers, and a group is a heading.
 *
 * A typed value is clamped by the Go side to the plugin's hard range, and the box
 * shows what came back rather than what was typed, so the user always sees the value
 * the plugin actually holds.
 */

import { useCallback, useEffect, useState } from "react";
import { chainApi, type ParamDTO } from "../api/document";
import ParamNumber, { ParamScrubLabel } from "../widgets/ParamNumber";
import Select from "../widgets/Select";
import TooltipHint from "../widgets/Tooltip";
import { buildParamTree, isContainer, type ParamNode } from "./paramTree";

interface Props {
  /** Bumped by the shell when a render should be redone. */
  selected: number;
  onChange: () => void;
}

/** isEditable reports whether a kind has a control the user can change. */
function isEditable(param: ParamDTO): boolean {
  switch (param.kind) {
    case "integer":
    case "double":
    case "boolean":
    case "choice":
    case "string":
    case "secret":
    case "integer2D":
    case "double2D":
    case "integer3D":
    case "double3D":
    case "rgb":
    case "rgba":
      return true;
  }
  return false;
}

export default function ParametersPanel({ selected, onChange }: Props) {
  const [params, setParams] = useState<ParamDTO[]>([]);
  const [error, setError] = useState("");

  const reload = useCallback(async () => {
    if (selected < 0) {
      setParams([]);
      return;
    }
    const list = await chainApi.params(selected);
    setParams(list ?? []);
  }, [selected]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const edit = async (action: () => Promise<string>) => {
    const result = await action();
    setError(result);
    await reload();
    onChange();
  };

  const resetAll = async () => {
    const [count, message] = await chainApi.resetParams(selected);
    setError(message);
    if (message === "") {
      setError("");
    }
    void count;
    await reload();
    onChange();
  };

  if (selected < 0) {
    return (
      <section className="panel panel--parameters" aria-label="Parameters">
        <h2 className="panel__title">Parameters</h2>
        <p className="panel__placeholder">Select a plugin in the chain.</p>
      </section>
    );
  }

  // A secret parameter is not drawn, so it must not be counted here either: the reset
  // button would otherwise stay enabled for a plugin whose every control is hidden.
  const editable = params.filter((param) => !param.secret && isEditable(param));
  // The tree is rebuilt on every load, which is once per edit. It is a few passes
  // over at most a few hundred parameters.
  const tree = buildParamTree(params);

  return (
    <section className="panel panel--parameters" aria-label="Parameters">
      <div className="params__header">
        <h2 className="panel__title">Parameters</h2>
        <button className="button" onClick={() => void resetAll()} disabled={editable.length === 0}>
          Reset all
        </button>
      </div>

      {error ? <p className="plugins__error">{error}</p> : null}
      {editable.length === 0 ? <p className="panel__placeholder">This plugin has no editable parameters.</p> : null}

      {tree.hiddenCount > 0 ? (
        <p className="params__hidden">
          {tree.hiddenCount} parameter{tree.hiddenCount === 1 ? "" : "s"} hidden by the plugin.
        </p>
      ) : null}

      {tree.roots.map((node) => (
        <ParamBranch
          key={node.param.name}
          node={node}
          depth={0}
          onEdit={(name, values) => void edit(() => chainApi.setParam(selected, name, values))}
          onEditText={(name, value) => void edit(() => chainApi.setParamText(selected, name, value))}
        />
      ))}
    </section>
  );
}

/**
 * ParamBranch draws one parameter and, when it is a group, everything inside it.
 *
 * The nesting comes from the plugin: it names each parameter's containing group, and
 * a group can hold another group. A flat list put every group heading at the top and
 * every control under it, which is what made a 49-group plugin unreadable.
 *
 * Groups are drawn as a <details> so a user can fold the sections they are not using.
 * They start closed: a plugin can declare forty-nine groups, and opening all of them by
 * default buries the one the user came for. The headings are the map; the controls are
 * one click away.
 */
function ParamBranch({
  node,
  depth,
  onEdit,
  onEditText,
}: {
  node: ParamNode;
  depth: number;
  onEdit: (name: string, values: number[]) => void;
  onEditText: (name: string, value: string) => void;
}) {
  const { param, children } = node;

  if (isContainer(param.kind)) {
    return (
      <details className="params__section" data-depth={depth}>
        <summary className="params__group">{param.label}</summary>
        {children.map((child) => (
          <ParamBranch
            key={child.param.name}
            node={child}
            depth={depth + 1}
            onEdit={onEdit}
            onEditText={onEditText}
          />
        ))}
      </details>
    );
  }

  if (!isEditable(param)) {
    return null;
  }
  return (
    <ParamRow
      param={param}
      onEdit={(values) => onEdit(param.name, values)}
      onEditText={(value) => onEditText(param.name, value)}
    />
  );
}

interface RowProps {
  param: ParamDTO;
  onEdit: (values: number[]) => void;
  onEditText: (value: string) => void;
}

/** ParamRow draws one control, chosen by the kind the plugin declared. */
function ParamRow({ param, onEdit, onEditText }: RowProps) {
  const disabled = !param.enabled;

  if (param.kind === "boolean") {
    const checked = (param.nums?.[0] ?? 0) !== 0;
    return (
      <label className="params__row">
        <TooltipHint text={param.hint}>
          <span className="params__label">{param.label}</span>
        </TooltipHint>
        <input
          type="checkbox"
          checked={checked}
          disabled={disabled}
          onChange={(event) => onEdit([event.target.checked ? 1 : 0])}
        />
      </label>
    );
  }

  if (param.kind === "choice") {
    const current = Math.round(param.nums?.[0] ?? 0);
    return (
      <div className="params__row">
        <TooltipHint text={param.hint}>
          <span className="params__label">{param.label}</span>
        </TooltipHint>
        <Select
          label={param.label}
          value={String(current)}
          disabled={disabled}
          onChange={(next) => onEdit([Number(next)])}
          // A plugin can offer forty-seven presets here, which is the case the native
          // popup handled worst.
          options={(param.choices ?? []).map((label, index) => ({
            label,
            value: String(index),
          }))}
        />
      </div>
    );
  }

  if (param.kind === "string" || param.kind === "secret") {
    return (
      <label className="params__row">
        <TooltipHint text={param.hint}>
          <span className="params__label">{param.label}</span>
        </TooltipHint>
        <input
          className="input"
          type={param.kind === "secret" ? "password" : "text"}
          defaultValue={param.text}
          disabled={disabled}
          onBlur={(event) => onEditText(event.target.value)}
        />
      </label>
    );
  }

  // Every numeric shape: one or more components with a hard range.
  const values = param.nums ?? new Array(param.dimension || 1).fill(0);
  const isInt = param.kind.startsWith("integer");
  const single = values.length === 1;

  // The step is derived from the range the plugin advertises, so a drag moves a
  // useful distance on a 0..1 parameter and on a 0..65535 one alike.
  const span = Math.abs(param.displayMax - param.displayMin);
  const step = isInt ? 1 : span > 0 ? Math.max(span / 200, 1e-6) : 0.01;

  // The first component drives the draggable label: dragging the name is the quick
  // way to make a small change without aiming at the box.
  const scrubProps = {
    value: values[0],
    min: param.hardMin,
    max: param.hardMax,
    integer: isInt,
    step,
    disabled,
    label: param.label,
    onCommit: (next: number) => {
      const updated = values.slice();
      updated[0] = next;
      onEdit(updated);
    },
  };

  return (
    <div className="params__row">
      <ParamScrubLabel {...scrubProps}>{param.label}</ParamScrubLabel>
      <div className="params__controls">
        {single ? (
          <input
            className="input params__slider"
            type="range"
            min={param.displayMin}
            max={param.displayMax}
            step={isInt ? 1 : "any"}
            value={values[0]}
            disabled={disabled}
            aria-label={`${param.label} slider`}
            onChange={(event) => onEdit([Number(event.target.value)])}
          />
        ) : null}
        {values.map((value, component) => (
          <ParamNumber
            key={component}
            value={value}
            min={param.hardMin}
            max={param.hardMax}
            integer={isInt}
            step={step}
            disabled={disabled}
            label={values.length > 1 ? `${param.label} ${component + 1}` : param.label}
            onCommit={(next) => {
              const updated = values.slice();
              updated[component] = next;
              onEdit(updated);
            }}
          />
        ))}
      </div>
    </div>
  );
}
