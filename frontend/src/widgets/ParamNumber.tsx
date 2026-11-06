/**
 * A numeric parameter you can drag.
 *
 * This is the widget the Base UI decision was made for. Every parameter in this
 * program is a number, a plugin can declare a hundred and sixty-five of them, and a
 * slider alone makes a precise value hard to set while a text box alone makes an
 * exploratory change slow.
 *
 * Base UI's ScrubArea gives the gesture: drag the label to change the value, hold
 * shift for a finer step. Pointer lock keeps the drag working past the window edge.
 * If the embedded webview refuses pointer lock the drag still works, it simply stops
 * at the edge, so the control degrades instead of breaking.
 *
 * The value is committed on release, not on every pointer move. A render runs the
 * whole plugin chain, so committing mid-drag would queue a render per pixel.
 */

import { NumberField } from "@base-ui/react/number-field";

interface Props {
  /** The value the plugin currently holds. */
  value: number;
  /** The lowest value the plugin accepts. */
  min: number;
  /** The highest value the plugin accepts. */
  max: number;
  /** True when the plugin declared an integer. */
  integer: boolean;
  /** The step a drag or an arrow key applies. */
  step: number;
  disabled: boolean;
  /** The accessible name, shown when the row label is not visible. */
  label: string;
  /** Called once, when the user releases the drag or leaves the field. */
  onCommit: (value: number) => void;
}

export default function ParamNumber({
  value,
  min,
  max,
  integer,
  step,
  disabled,
  label,
  onCommit,
}: Props) {
  return (
    <NumberField.Root
      value={value}
      min={min}
      max={max}
      step={step}
      // A large step for the buttons and a small one for the drag keeps both
      // gestures usable on a 0..1 parameter and on a 0..255 one.
      smallStep={integer ? 1 : step / 10}
      largeStep={integer ? 10 : step * 10}
      disabled={disabled}
      // Only a released value is committed, so a drag costs one render rather than
      // one per pointer move.
      onValueCommitted={(next) => {
        if (next !== null && next !== value) {
          onCommit(next);
        }
      }}
    >
      <NumberField.Group className="numfield">
        <NumberField.Decrement className="numfield__step" aria-label={`Decrease ${label}`}>
          −
        </NumberField.Decrement>
        <NumberField.Input className="input numfield__input" aria-label={label} />
        <NumberField.Increment className="numfield__step" aria-label={`Increase ${label}`}>
          +
        </NumberField.Increment>
      </NumberField.Group>
    </NumberField.Root>
  );
}

/**
 * ParamScrubLabel is the draggable name of a parameter.
 *
 * The label is the drag handle because a slider already claims the row for its own
 * drag. Keeping them apart means a user can set a rough value with the slider and a
 * precise one by dragging the name, without the two gestures fighting.
 */
export function ParamScrubLabel({
  value,
  min,
  max,
  integer,
  step,
  disabled,
  label,
  onCommit,
  children,
}: Props & { children: React.ReactNode }) {
  return (
    <NumberField.Root
      value={value}
      min={min}
      max={max}
      step={step}
      smallStep={integer ? 1 : step / 10}
      largeStep={integer ? 10 : step * 10}
      disabled={disabled}
      onValueCommitted={(next) => {
        if (next !== null && next !== value) {
          onCommit(next);
        }
      }}
    >
      <NumberField.ScrubArea className="params__label params__label--scrub">
        {children}
      </NumberField.ScrubArea>
    </NumberField.Root>
  );
}
