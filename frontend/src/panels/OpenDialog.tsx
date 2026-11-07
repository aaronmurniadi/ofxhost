/**
 * The dialog the working space is chosen in.
 *
 * Decision D8: a RAW file declares no colour space, so the space is a choice about
 * how to interpret the sensor, not a property to read. The choice is an input to the
 * decode, which is why it is asked before the file opens and not offered as a control
 * afterwards: the pixels are developed into it, and relabelling them later would tell
 * the chain a space the pixels are not in.
 *
 * Changing the space means opening the file again, and the dialog says so rather than
 * implying a live switch.
 */

import Modal from "../widgets/Modal";
import { Button } from "@cloudflare/kumo/components/button";
import { Radio, RadioGroup } from "@cloudflare/kumo/components/radio";
import { useEffect, useState } from "react";
import { api } from "../api/document";

interface Props {
  /** The file waiting to be opened. */
  path: string;
  /** Called with the chosen space index. */
  onConfirm: (space: number) => void;
  onCancel: () => void;
}

/** storageKey remembers the last choice, so the common case is one click. */
const storageKey = "ofxhost.workingSpace.v1";

export default function OpenDialog({ path, onConfirm, onCancel }: Props) {
  const [spaces, setSpaces] = useState<string[]>([]);
  const [choice, setChoice] = useState(0);

  useEffect(() => {
    void (async () => {
      const available = await api.colorSpaces();
      setSpaces(available);
      const remembered = window.localStorage.getItem(storageKey);
      if (remembered !== null) {
        const index = Number(remembered);
        if (Number.isInteger(index) && index >= 0 && index < available.length) {
          setChoice(index);
        }
      }
    })();
  }, []);

  const confirm = () => {
    window.localStorage.setItem(storageKey, String(choice));
    onConfirm(choice);
  };

  const fileName = path.split(/[\\/]/).pop() ?? path;

  return (
    <Modal
      open
      onClose={onCancel}
      title="Working space"
      description={`${fileName} is a RAW file, so it declares no colour space. Choose the space to develop it into. This is the space the chain works in, and the space the first plugin is given.`}
      actions={
        <>
          <Button variant="primary" size="sm" onClick={confirm}>
            Open
          </Button>
          <Button variant="secondary" size="sm" onClick={onCancel}>
            Cancel
          </Button>
        </>
      }
    >
      {/* A card group rather than a column of dots: the four spaces are a choice between
          named options, and the card treatment makes the selected one legible at a glance. */}
      <RadioGroup<string>
        legend="Working space"
        value={String(choice)}
        onValueChange={(next) => setChoice(Number(next))}
        appearance="card"
        orientation="vertical"
      >
        {spaces.map((name, index) => (
          <Radio.Item key={name} value={String(index)} label={name} />
        ))}
      </RadioGroup>

      <p className="modal__note">
        Changing this later means opening the file again: the space is where the decode
        happens, not a setting applied afterwards.
      </p>
    </Modal>
  );
}
