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
{
  # exportCatalog: modules -> [ { name; type; description; default?; riskClass? } ]
  # Evaluate the module set only for its option declarations.
  exportCatalog = { modules }:
    let
      eval = lib.evalModules {
        modules = modules ++ [{ _module.check = false; }];
      };
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
      dawoOpts = (eval.options.dawo or { });
    in
    walk [ ] dawoOpts;
}
