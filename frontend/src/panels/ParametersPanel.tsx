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

  const editable = params.filter(isEditable);

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

      {params.map((param) => {
        if (param.kind === "group" || param.kind === "page") {
          return (
            <h3 key={param.name} className="params__group">
              {param.label}
            </h3>
          );
        }
        if (!isEditable(param)) {
          return null;
        }
        return (
          <ParamRow
            key={param.name}
            param={param}
            onEdit={(values) => void edit(() => chainApi.setParam(selected, param.name, values))}
            onEditText={(value) => void edit(() => chainApi.setParamText(selected, param.name, value))}
          />
        );
      })}
    </section>
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
        <span className="params__label" title={param.hint}>
          {param.label}
        </span>
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
      <label className="params__row">
        <span className="params__label" title={param.hint}>
          {param.label}
        </span>
        <select
          className="input"
          value={current}
          disabled={disabled}
          onChange={(event) => onEdit([Number(event.target.value)])}
        >
          {(param.choices ?? []).map((label, index) => (
            <option key={`${index}-${label}`} value={index}>
              {label}
            </option>
          ))}
        </select>
      </label>
    );
  }

  if (param.kind === "string" || param.kind === "secret") {
    return (
      <label className="params__row">
        <span className="params__label" title={param.hint}>
          {param.label}
        </span>
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

  return (
    <div className="params__row">
      <span className="params__label" title={param.hint}>
        {param.label}
      </span>
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
            onChange={(event) => onEdit([Number(event.target.value)])}
          />
        ) : null}
        {values.map((value, component) => (
          <input
            key={component}
            className="input params__number"
            type="number"
            min={param.hardMin}
            max={param.hardMax}
            step={isInt ? 1 : "any"}
            value={value}
            disabled={disabled}
            onChange={(event) => {
              const next = values.slice();
              next[component] = Number(event.target.value);
              onEdit(next);
            }}
          />
        ))}
      </div>
    </div>
  );
}
