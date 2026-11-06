/**
 * The modal shell.
 *
 * Every dialog in the program is this component. It exists because the hand-written
 * markup it replaces had none of the behaviour a dialog needs: focus did not move into
 * it, Escape did not close it, Tab walked out into the panel behind, the page could
 * still scroll underneath, and a screen reader was never told anything had opened.
 *
 * Base UI supplies all of that, including returning focus to whatever opened the
 * dialog. Only the appearance is ours.
 */

import { Dialog } from "@base-ui/react/dialog";
import type { ReactNode } from "react";

export interface ModalProps {
  /** Whether the dialog is open. The caller owns this. */
  open: boolean;
  /** Called when the user dismisses it with Escape or the backdrop. */
  onClose: () => void;
  /** The heading, also used as the dialog's accessible name. */
  title: string;
  /** A short line under the heading. It is read as the dialog's description. */
  description?: string;
  /** The dialog's body. */
  children: ReactNode;
  /** The button row along the bottom. */
  actions?: ReactNode;
  /** Draws the body wider, for the export dialog's two-column layout. */
  wide?: boolean;
}

export default function Modal({
  open,
  onClose,
  title,
  description,
  children,
  actions,
  wide = false,
}: ModalProps) {
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        // Base UI reports every way of closing, including a click on the backdrop.
        // The caller decides what closing means, so it is told rather than assumed.
        if (!next) {
          onClose();
        }
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className="modal-backdrop" />
        <Dialog.Viewport className="modal-viewport">
          <Dialog.Popup className={wide ? "modal modal--wide" : "modal"}>
            <Dialog.Title className="modal__title">{title}</Dialog.Title>
            {description !== undefined ? (
              <Dialog.Description className="modal__text">{description}</Dialog.Description>
            ) : null}

            <div className="modal__body">{children}</div>

            {actions !== undefined ? <div className="modal__actions">{actions}</div> : null}
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
