/**
 * The parameter tree.
 *
 * An OpenFX plugin declares a flat list of parameters, and each one names its
 * containing group through `parent`. A panel that draws the list in order puts every
 * control at the top level and scatters each group's heading far from its members,
 * which is what made a plugin with 49 groups and 830 parameters unreadable.
 *
 * This module turns the flat list into the tree the plugin described.
 */

import type { ParamDTO } from "../api/document";

/** ParamNode is one parameter and the parameters inside it, when it is a group. */
export interface ParamNode {
  param: ParamDTO;
  children: ParamNode[];
  /**
   * hidden reports that the plugin marked this parameter, or a group that holds it,
   * secret. The node is drawn only when the caller asked for the secret ones.
   */
  hidden: boolean;
}

/** ParamTree is what the panel draws. */
export interface ParamTree {
  /** Nodes with no parent, in the order the plugin declared them. */
  roots: ParamNode[];
  /** How many parameters the plugin marked secret, so the panel can say so. */
  hiddenCount: number;
}

/** ParamTreeOptions changes what the tree keeps. */
export interface ParamTreeOptions {
  /**
   * includeSecret draws the parameters the plugin marked secret.
   *
   * The default is to drop them: OpenFX reserves the flag for parameters a plugin
   * controls through its own overlay. A user who wants to see the whole state anyway
   * turns it on, and the group kinds are kept so the subtree still nests.
   */
  includeSecret?: boolean;
}

/** isContainer reports whether a kind holds other parameters. */
export function isContainer(kind: string): boolean {
  return kind === "group" || kind === "page";
}

/**
 * buildParamTree nests the parameters the way the plugin declared them.
 *
 * A secret parameter is dropped by default, together with everything inside it: OpenFX
 * reserves the flag for parameters a plugin controls through its own overlay, and vendors
 * also use it to gate a feature that is not licensed in a given edition. Drawing them
 * unasked would show a password box for a group. ParamTreeOptions.includeSecret keeps
 * them instead, with their real kinds, so a user can inspect the full state.
 *
 * Two things must not lose data. A parameter naming a parent that does not exist is
 * promoted to the top level rather than dropped, and a parent cycle is broken at the
 * point it closes, because a hand-edited settings file or an unusual plugin must not
 * hang the panel.
 */
export function buildParamTree(params: ParamDTO[], options: ParamTreeOptions = {}): ParamTree {
  const includeSecret = options.includeSecret === true;

  const hidden = new Set<string>();
  for (const param of params) {
    if (!param.secret) {
      continue;
    }
    hidden.add(param.name);
    // A hidden group hides its members, so the set is closed downwards. The walk
    // stops when nothing new is added, which also terminates on a cycle.
    let changed = true;
    while (changed) {
      changed = false;
      for (const candidate of params) {
        if (hidden.has(candidate.name)) {
          continue;
        }
        if (hidden.has(candidate.parent)) {
          hidden.add(candidate.name);
          changed = true;
        }
      }
    }
  }

  const nodes = new Map<string, ParamNode>();
  for (const param of params) {
    const isHidden = hidden.has(param.name);
    if (isHidden && !includeSecret) {
      continue;
    }
    nodes.set(param.name, { param, children: [], hidden: isHidden });
  }

  const roots: ParamNode[] = [];
  for (const param of params) {
    const node = nodes.get(param.name);
    if (node === undefined) {
      continue;
    }
    const parent = nodes.get(param.parent);
    if (parent === undefined || !isContainer(parent.param.kind)) {
      // No parent, or a parent that is not a container: this parameter belongs at
      // the top level. Promoting it keeps a malformed declaration visible instead of
      // silently dropping the control.
      roots.push(node);
      continue;
    }
    parent.children.push(node);
  }

  breakCycles(roots);

  return { roots, hiddenCount: hidden.size };
}

/**
 * breakCycles flattens any node reachable from itself.
 *
 * A cycle cannot be drawn, and drawing it would recurse until the stack gave out. The
 * node where the cycle closes keeps its own children and the rest are lifted, so no
 * control disappears.
 */
function breakCycles(roots: ParamNode[]): void {
  const visiting = new Set<ParamNode>();

  const walk = (node: ParamNode): void => {
    if (visiting.has(node)) {
      node.children = [];
      return;
    }
    visiting.add(node);
    for (const child of node.children) {
      walk(child);
    }
    visiting.delete(node);
  };

  for (const root of roots) {
    walk(root);
  }
}
