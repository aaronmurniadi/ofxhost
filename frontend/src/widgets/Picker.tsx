/**
 * A searchable dropdown, on Base UI's Combobox.
 *
 * The plugin list answers two questions at once. It is short when only a few plugins are
 * installed, and long when a machine has seventy, so the popup has to be both a dropdown and
 * a filter. A plain `Select` gives no way to search, and a list drawn in the panel takes
 * room away from the chain - which is why this is a popup that closes on a choice.
 *
 * The names are sorted alphabetically. A machine can have seventy plugins and the order a
 * host discovers them in is not an order a user can predict, so the list is sorted once
 * here: the plugin a user learned the place of stays in that place.
 *
 * Only the look is ours. Base UI supplies the keyboard model (arrow keys move the highlight,
 * Enter picks, Escape closes), the ARIA roles, and the filtering.
 *
 * The selection is deliberately not kept. The control adds a plugin; it does not hold a
 * current one. Leaving the choice selected would filter the next opening to the plugin the
 * user already added, so `value` stays null and the query is cleared on both paths.
 */

import { Combobox } from "@base-ui/react/combobox";
import { useState } from "react";

export interface PickerItem {
  /** The value reported when the item is chosen. */
  value: string;
  /** The text the user sees and types against. */
  label: string;
  /** Extra text the search also matches, which the list does not show. */
  author?: string;
  /** Extra text the search also matches, which the list does not show. */
  identifier?: string;
}

/**
 * matches is the filter, and it covers more than the label.
 *
 * A user looking for a plugin may know its author or its identifier rather than the name the
 * menu shows. Matching only the label made those searches return nothing, which reads as
 * "not installed".
 */
export function matches(item: PickerItem, query: string): boolean {
  const needle = query.trim().toLowerCase();
  if (needle === "") {
    return true;
  }
  return [item.label, item.author ?? "", item.identifier ?? ""].some((text) =>
    text.toLowerCase().includes(needle),
  );
}

/**
 * sortByLabel returns the items in alphabetical order, without touching the caller's array.
 *
 * Sorted by the label the user reads, case-insensitively, so "denoise" and "Denoise" land in
 * the same place whichever a plugin author typed.
 */
export function sortByLabel(items: PickerItem[]): PickerItem[] {
  return items.slice().sort((left, right) => left.label.localeCompare(right.label, undefined, { sensitivity: "base" }));
}

export interface PickerProps {
  items: PickerItem[];
  /** Called with the chosen item's value. */
  onSelect: (value: string) => void;
  /** The trigger's text: the thing the control adds. */
  label: string;
  /** The text shown while the search box is empty. */
  placeholder?: string;
  disabled?: boolean;
}

export default function Picker({
  items,
  onSelect,
  label,
  placeholder = "Search…",
  disabled = false,
}: PickerProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");

  const close = () => {
    setOpen(false);
    setQuery("");
  };

  const choices = sortByLabel(items);

  return (
    <Combobox.Root
      items={choices}
      // The control adds one item and forgets it, so nothing is ever selected.
      value={null}
      onValueChange={(next) => {
        if (next === null) {
          return;
        }
        const chosen = next as PickerItem;
        onSelect(chosen.value);
        close();
      }}
      open={open}
      onOpenChange={(next) => {
        if (next) {
          setOpen(true);
          return;
        }
        close();
      }}
      inputValue={query}
      onInputValueChange={setQuery}
      filter={matches}
      disabled={disabled}
    >
      <Combobox.Trigger className="button" aria-label={label}>
        {label}
        <Combobox.Icon className="picker__icon" aria-hidden>
          ▾
        </Combobox.Icon>
      </Combobox.Trigger>

      <Combobox.Portal>
        <Combobox.Positioner className="picker__positioner" sideOffset={4} align="start">
          <Combobox.Popup className="picker__popup" aria-label={label}>
            {/* The search box lives inside the popup, so the trigger stays a button and
                the list cannot be filtered without the popup being open. */}
            <Combobox.Input className="input picker__input" placeholder={placeholder} />
            <Combobox.Empty>
              {/* Two different facts, two different sentences: a machine with no plugins
                  installed and a search that found nothing are not the same problem. */}
              <p className="panel__placeholder picker__empty">
                {items.length === 0 ? "No plugins were found" : "No matches"}
              </p>
            </Combobox.Empty>
            <Combobox.List className="picker__list">
              {choices.map((item) => (
                <Combobox.Item key={item.value} value={item} className="picker__item">
                  {item.label}
                </Combobox.Item>
              ))}
            </Combobox.List>
          </Combobox.Popup>
        </Combobox.Positioner>
      </Combobox.Portal>
    </Combobox.Root>
  );
}
