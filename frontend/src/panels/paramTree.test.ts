import { describe, expect, it } from "vitest";
import { buildParamTree, filterParamTree, isContainer } from "./paramTree";
import type { ParamDTO } from "../api/document";

/** param builds the smallest ParamDTO the tree cares about. */
function param(name: string, kind: string, parent = "", secret = false): ParamDTO {
  return {
    name,
    label: name,
    hint: "",
    parent,
    kind,
    secret,
    dimension: 1,
    nums: [0],
    text: "",
    choices: null,
    displayMin: 0,
    displayMax: 1,
    hardMin: 0,
    hardMax: 1,
    enabled: true,
  };
}

describe("buildParamTree", () => {
  it("nests a parameter under the group that names it", () => {
    const tree = buildParamTree([
      param("colorGroup", "group"),
      param("exposure", "double", "colorGroup"),
      param("contrast", "double", "colorGroup"),
    ]);

    expect(tree.roots).toHaveLength(1);
    expect(tree.roots[0].param.name).toBe("colorGroup");
    expect(tree.roots[0].children.map((c) => c.param.name)).toEqual(["exposure", "contrast"]);
  });

  it("keeps a group inside a group", () => {
    // This is the shape spektrafilm declares: effectsGroup holds lensDistortionGroup.
    const tree = buildParamTree([
      param("effectsGroup", "group"),
      param("lensDistortionGroup", "group", "effectsGroup"),
      param("distortion", "double", "lensDistortionGroup"),
    ]);

    expect(tree.roots).toHaveLength(1);
    const effects = tree.roots[0];
    expect(effects.param.name).toBe("effectsGroup");
    expect(effects.children[0].param.name).toBe("lensDistortionGroup");
    expect(effects.children[0].children[0].param.name).toBe("distortion");
  });

  it("drops a secret parameter", () => {
    const tree = buildParamTree([param("colorGroup", "group"), param("hidden", "double", "colorGroup", true)]);

    expect(tree.roots[0].children).toHaveLength(0);
    expect(tree.hiddenCount).toBe(1);
  });

  it("drops everything inside a secret group", () => {
    // The case that mattered: spektrafilm marks a whole feature area secret, and its
    // members must go with it even when the plugin does not mark them individually.
    const tree = buildParamTree([
      param("technicolorGroup", "group", "", true),
      param("technicolorMatrixGroup", "group", "technicolorGroup", true),
      param("chem", "double", "technicolorMatrixGroup"),
    ]);

    expect(tree.roots).toHaveLength(0);
    expect(tree.hiddenCount).toBe(3);
  });

  it("gives a secret group its group kind rather than a type of its own", () => {
    // The original bug: secret became a kind, so a group turned into a text box and
    // the panel drew a password field where a heading belonged.
    const tree = buildParamTree([param("group", "group", "", true)]);
    expect(tree.hiddenCount).toBe(1);
    expect(isContainer("group")).toBe(true);
  });

  it("keeps a secret parameter when the caller asks for it", () => {
    const tree = buildParamTree(
      [param("colorGroup", "group"), param("hidden", "double", "colorGroup", true)],
      { includeSecret: true },
    );

    expect(tree.roots[0].children.map((c) => c.param.name)).toEqual(["hidden"]);
    expect(tree.roots[0].children[0].hidden).toBe(true);
    expect(tree.roots[0].hidden).toBe(false);
    // The count is of the parameters the plugin flagged, not of the ones dropped.
    expect(tree.hiddenCount).toBe(1);
  });

  it("keeps the members of a secret group nested under it", () => {
    // spektrafilm marks 390 of 830 parameters secret, most of them through a group that
    // holds them. They must come back as a labelled subtree, not as a flat list.
    const tree = buildParamTree(
      [
        param("technicolorGroup", "group", "", true),
        param("technicolorMatrixGroup", "group", "technicolorGroup", true),
        param("chem", "double", "technicolorMatrixGroup"),
      ],
      { includeSecret: true },
    );

    expect(tree.roots).toHaveLength(1);
    expect(tree.roots[0].param.name).toBe("technicolorGroup");
    expect(tree.roots[0].hidden).toBe(true);
    const matrix = tree.roots[0].children[0];
    expect(matrix.param.name).toBe("technicolorMatrixGroup");
    // The member is not flagged itself, but its container is, so it is marked too.
    expect(matrix.children[0].param.name).toBe("chem");
    expect(matrix.children[0].hidden).toBe(true);
  });

  it("promotes a parameter whose parent does not exist", () => {
    const tree = buildParamTree([param("orphan", "double", "missingGroup")]);
    expect(tree.roots.map((n) => n.param.name)).toEqual(["orphan"]);
  });

  it("promotes a parameter whose parent is not a container", () => {
    // A number cannot hold children, so a parameter naming it is lifted rather than
    // lost.
    const tree = buildParamTree([param("notAGroup", "double"), param("child", "double", "notAGroup")]);
    expect(tree.roots.map((n) => n.param.name)).toEqual(["notAGroup", "child"]);
  });

  it("survives a parent cycle without recursing forever", () => {
    // Drawing this would exhaust the stack, so the cycle is broken where it closes.
    const tree = buildParamTree([
      param("a", "group", "b"),
      param("b", "group", "a"),
      param("leaf", "double", "a"),
    ]);
    // Neither is a root, because each names the other.
    expect(tree.roots).toHaveLength(0);
    // The point is that the call returned at all.
    expect(tree.hiddenCount).toBe(0);
  });

  it("keeps the declared order of top-level parameters", () => {
    const tree = buildParamTree([
      param("quickAccessGroup", "group"),
      param("colorGroup", "group"),
      param("filmGroup", "group"),
    ]);
    expect(tree.roots.map((n) => n.param.name)).toEqual([
      "quickAccessGroup",
      "colorGroup",
      "filmGroup",
    ]);
  });
});

describe("filterParamTree", () => {
  /** nested builds the shape a plugin like spektrafilm declares: a group inside a group. */
  function nested() {
    return buildParamTree([
      param("effectsGroup", "group"),
      param("lensGroup", "group", "effectsGroup"),
      param("lensDistortion", "double", "lensGroup"),
      param("lensVignette", "double", "lensGroup"),
      param("exposure", "double", "effectsGroup"),
      param("grain", "double"),
    ]).roots;
  }

  it("keeps a match together with the groups that hold it", () => {
    const result = filterParamTree(nested(), "distortion");
    expect(result.matches).toBe(1);
    // Without its ancestors the control would appear at the top level, in no group.
    expect(result.roots.map((n) => n.param.name)).toEqual(["effectsGroup"]);
    expect(result.roots[0].children.map((n) => n.param.name)).toEqual(["lensGroup"]);
    expect(result.roots[0].children[0].children.map((n) => n.param.name)).toEqual([
      "lensDistortion",
    ]);
  });

  it("shows everything inside a group whose own name matches", () => {
    // A user who types a group name is asking what is in it, so the members stay.
    const result = filterParamTree(nested(), "lensgroup");
    expect(result.matches).toBe(1);
    expect(result.roots[0].children[0].children.map((n) => n.param.name)).toEqual([
      "lensDistortion",
      "lensVignette",
    ]);
  });

  it("ignores case and surrounding space", () => {
    expect(filterParamTree(nested(), "  EXPOSURE ").matches).toBe(1);
  });

  it("matches the parameter name as well as the label", () => {
    // A plugin's label can be a phrase while its name is the identifier a search can use.
    const roots = buildParamTree([param("isoGain", "double")]).roots;
    expect(filterParamTree(roots, "isogain").matches).toBe(1);
  });

  it("reports no match rather than dropping the tree silently", () => {
    const result = filterParamTree(nested(), "kaleidoscope");
    expect(result.matches).toBe(0);
    expect(result.roots).toHaveLength(0);
  });

  it("leaves the tree alone", () => {
    const roots = nested();
    filterParamTree(roots, "grain");
    // The panel keeps the full tree for the next keystroke, so a search must not rewrite it.
    expect(roots).toHaveLength(2);
    expect(roots[0].children).toHaveLength(2);
  });

  it("returns the whole tree for an empty query", () => {
    const roots = nested();
    expect(filterParamTree(roots, "   ").roots).toBe(roots);
  });
});
