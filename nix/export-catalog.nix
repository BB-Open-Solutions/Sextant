# export-catalog.nix - generates catalog.json (ADR 0005) from the real
# option tree: every documented option under `dawo.` becomes a catalog
# entry the console renders. No annotations, no entry - undocumented
# options stay engineer-only by design.
#
# Optional per-option annotations (appended after mkOption, since mkOption
# rejects unknown arguments):
#   lib.mkOption { ... } // { riskClass = "high"; }
# riskClass surfaces in the console as a warning badge; the vocabulary is
# free-form by design (orgs classify differently), "high" is the convention.
{ lib }:
let
  # Only JSON-representable defaults are exported; a derivation, function
  # or self-referential default is real but not renderable, so it is
  # omitted rather than crashing the export. The walk is depth-bounded
  # and never deepSeqs: full NixOS defaults can be enormous or cyclic
  # (tryEval cannot catch a stack overflow, only a throw).
  isPlain = depth: v:
    depth > 0 && (
      v == null || lib.isBool v || lib.isInt v || lib.isFloat v
      || lib.isString v
      || (lib.isList v && lib.all (isPlain (depth - 1)) v)
      || (lib.isAttrs v
        && !(v ? _type)
        && !(lib.isDerivation v)
        && lib.all (isPlain (depth - 1)) (lib.attrValues v))
    );
  plainDefault = opt:
    let
      hasDefault = opt ? default;
      ok = builtins.tryEval (hasDefault && isPlain 4 opt.default);
    in
    if ok.success && ok.value
    then { default = opt.default; }
    # The option DOES have a default - it is just too deep/non-plain to
    # serialise (or evaluating it threw). Without this branch the console
    # renders the field as if it had no default at all, which can diverge
    # from what the gate actually applies; mark it explicitly instead so the
    # console can say "has a default (not shown)" rather than "no default".
    else if hasDefault then { defaultOmitted = true; }
    else { };
  # managedPrefixes: option subtrees Sextant itself wires and therefore NEVER
  # exports to the operator catalog. The update funnel is the canonical case:
  # the generator/addon sets autoUpdate's repoUrl and branch per device
  # (rings/<group>, ADR 0011) - an operator-facing knob there would let a
  # settings edit silently detach devices from the funnel Sextant manages.
  # Engineers still see these options in the overlay source; they are
  # plumbing, not policy.
  managedPrefixes = [
    [ "autoUpdate" ] # the update funnel Sextant owns (rings/<group>)
    [ "secureboot" "pkiBundle" ] # sbctl PKI path: a fixed convention, not policy
    [ "diskUnlock" "luksVolume" ] # disko layout name; wrong value bricks unlock
  ];
  isManaged = path:
    lib.any (p: lib.take (lib.length p) path == p) managedPrefixes;
  riskClass = opt:
    if (opt.riskClass or "") != "" then { riskClass = opt.riskClass; } else { };
  # secret: an option annotated `// { secret = true; }` renders in the console
  # as a secret-ref picker (the value is a secret name, never the material).
  secret = opt:
    if (opt.secret or false) then { secret = true; } else { };
  walk = prefix: opts:
    lib.concatLists (lib.mapAttrsToList
      (name: v:
        let path = prefix ++ [ name ]; in
        if lib.isOption v then
          lib.optional (v ? description && v.description != null) ({
            name = lib.concatStringsSep "." path;
            type = v.type.description or "unknown";
            description =
              if lib.isString v.description
              then v.description
              else v.description.text or "";
          } // plainDefault v // riskClass v // secret v)
        else if lib.isAttrs v then walk path v
        else [ ])
      opts);
in
{
  # exportCatalogFromOptions: options -> catalog entries. Takes the option
  # tree of an ALREADY-EVALUATED host (nixosConfigurations.<x>.options), so
  # full NixOS module sets export without re-evaluating them standalone -
  # evalModules cannot handle real NixOS blocks (they need the whole module
  # system), a finished host evaluation already did the hard part.
  exportCatalogFromOptions = options: walk [ ] (options.dawo or { });

  # exportCatalog: modules -> [ { name; type; description; default?; riskClass? } ]
  # Evaluate a STANDALONE module set for its option declarations (miniature
  # cores, tests). Full NixOS module sets should use
  # exportCatalogFromOptions on an evaluated host instead. specialArgs
  # carries whatever the module files take as arguments.
  exportCatalog = { modules, specialArgs ? { } }:
    let
      eval = lib.evalModules {
        modules = modules ++ [{ _module.check = false; }];
        inherit specialArgs;
      };
    in
    walk [ ] (eval.options.dawo or { });
}
