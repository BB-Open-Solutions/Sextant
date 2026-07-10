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
  # Only JSON-representable defaults are exported; a derivation or
  # function default is real but not renderable, so it is omitted
  # rather than crashing the export.
  isPlain = v:
    v == null || lib.isBool v || lib.isInt v || lib.isFloat v
    || lib.isString v
    || (lib.isList v && lib.all isPlain v)
    || (lib.isAttrs v && !(v ? _type) && lib.all isPlain (lib.attrValues v));
  plainDefault = opt:
    let forced = builtins.tryEval (builtins.deepSeq (opt.default or null) (opt.default or null));
    in if (opt ? default) && forced.success && isPlain forced.value
    then { default = forced.value; }
    else { };
  riskClass = opt:
    if (opt.riskClass or "") != "" then { riskClass = opt.riskClass; } else { };
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
          } // plainDefault v // riskClass v)
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
