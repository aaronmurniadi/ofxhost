/**
 * A dropdown, on Base UI.
 *
 * The native `<select>` is replaced for two reasons. A list can be long — a plugin can
 * offer forty-seven presets and the colour-space list has thirty-three entries — and the
 * native popup gives no way to search or to show a scroll context. The other reason is
 * that the native popup cannot be styled, so it was the one control that ignored the
 * theme.
 *
 * Base UI supplies the keyboard model, typeahead, and the ARIA roles; only the look is
 * ours.
 */

import { Select as BaseSelect } from "@base-ui/react/select";

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
    <BaseSelect.Root
      value={value}
      // The items are declared here as well as rendered below, and that is not
      // duplication. The rendered items live in a portal that is not mounted while the
      // popup is closed, so the trigger has nothing to read a label from and falls back
      // to the raw value - which showed "0" instead of the option's name in every
      // dropdown.
      items={options}
      onValueChange={(next) => {
        // Base UI reports null when the selection is cleared, which this control never
        // offers, so it is ignored rather than treated as a choice of "nothing".
        if (next !== null) {
          onChange(next as string);
        }
      }}
      disabled={disabled}
    >
      <BaseSelect.Trigger id={id} className="select" aria-label={label} aria-labelledby={labelId}>
        <BaseSelect.Value className="select__value" />
        <BaseSelect.Icon className="select__icon" aria-hidden>
          ▾
        </BaseSelect.Icon>
      </BaseSelect.Trigger>

      <BaseSelect.Portal>
        <BaseSelect.Positioner className="select__positioner" sideOffset={4}>
          <BaseSelect.Popup className="select__popup">
            <BaseSelect.List className="select__list">
              {options.map((option) => (
                <BaseSelect.Item key={option.value} value={option.value} className="select__item">
                  <BaseSelect.ItemIndicator className="select__tick" aria-hidden>
                    ✓
                  </BaseSelect.ItemIndicator>
                  <BaseSelect.ItemText>{option.label}</BaseSelect.ItemText>
                </BaseSelect.Item>
              ))}
            </BaseSelect.List>
          </BaseSelect.Popup>
        </BaseSelect.Positioner>
      </BaseSelect.Portal>
    </BaseSelect.Root>
  );
}
