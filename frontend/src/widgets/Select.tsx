/**
 * A dropdown, on Kumo.
 *
 * Kumo's Select is one component rather than an assembly of parts: it takes the options,
 * the value and the change handler, and builds the trigger, the popup and the list itself.
 * That replaced about eighty lines of Base UI plumbing with an adapter whose only job is to
 * keep the prop names this project uses.
 *
 * Two deliberate choices:
 *
 * `aria-label` is passed rather than `label`, because Kumo renders a visible label above
 * the control when given one, and every caller here already draws its own label beside the
 * control. Two visible labels for one field is worse than none.
 *
 * The value stays a `string`. Kumo reports null when a selection is cleared, which this
 * control never offers, so null is ignored rather than read as a choice of nothing.
 */

import { Select as KumoSelect } from "@cloudflare/kumo/components/select";

export interface SelectOption {
  /** The display text. */
  label: string;
  /** The value reported when chosen. */
  value: string;
}

export interface SelectProps {
  /** The chosen value. */
  value: string;
  /** Called with the value of the chosen option. */
  onChange: (value: string) => void;
  options: SelectOption[];
  /** The accessible name, used when no visible label is bound to this control. */
  label: string;
  /** Id of a visible label element, when there is one. */
  labelId?: string;
  disabled?: boolean;
  /** Matches the control to a visible <label htmlFor>. */
  id?: string;
}

export default function Select({
  value,
  onChange,
  options,
  label,
  labelId,
  disabled = false,
  id,
}: SelectProps) {
  return (
    <KumoSelect
      id={id}
      aria-label={labelId === undefined ? label : undefined}
      aria-labelledby={labelId}
      items={options}
      value={value}
      onValueChange={(next) => {
        if (next !== null) {
          onChange(String(next));
        }
      }}
      disabled={disabled}
      size="sm"
      className="ofxhost-select"
    />
  );
}
