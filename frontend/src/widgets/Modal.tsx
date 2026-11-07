/**
 * The modal shell.
 *
 * Every dialog in the program is this component. It is Kumo's Dialog, which renders the
 * backdrop, the portal and the focus trap itself.
 *
 * The hand-written markup this replaced had none of the behaviour a dialog needs: focus
 * did not move into it, Escape did nothing, Tab walked into the panel behind, and the page
 * scrolled underneath. Base UI supplied that; Kumo supplies the same behaviour with its own
 * appearance, which is why this file is shorter than the one it replaced.
 *
 * Kumo sizes its dialogs rather than taking a width, so `wide` picks the larger box instead
 * of setting pixels. A dialog that sets its own width would drift from the design system the
 * first time Kumo changed a breakpoint.
 */

// Imported from its own module rather than the barrel. The barrel pulls every Kumo
// component, chart and icon into the bundle whether it is used or not, which took the
// production bundle from 497 KB to 1 MB.
import { Dialog } from "@cloudflare/kumo/components/dialog";
import type { ReactNode } from "react";

export interface ModalProps {
  /** Whether the dialog is open. The caller owns this. */
  open: boolean;
  /** Called when the user dismisses it with Escape, the backdrop, or a close control. */
  onClose: () => void;
  /** The heading, also used as the dialog's accessible name. */
  title: string;
  /** A short line under the heading, read as the dialog's description. */
  description?: string;
  /** The dialog's body. */
  children: ReactNode;
  /** The button row along the bottom. */
  actions?: ReactNode;
  /** Chooses the extra-large box, for the export dialog's two-column layout. */
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
        // Kumo reports every way of closing, including the backdrop and Escape. The
        // caller decides what closing means, so it is told rather than assumed.
        if (!next) {
          onClose();
        }
      }}
    >
      <Dialog size={wide ? "xl" : "lg"} className="ofxhost-modal">
        <Dialog.Title className="modal__title">{title}</Dialog.Title>
        {description !== undefined ? (
          <Dialog.Description className="modal__text">{description}</Dialog.Description>
        ) : null}

        <div className="modal__body">{children}</div>

        {actions !== undefined ? <div className="modal__actions">{actions}</div> : null}
      </Dialog>
    </Dialog.Root>
  );
}
