# export-catalog.nix - generates catalog.json (ADR 0005) from the real
# option tree: every documented option under `dawo.` becomes a catalog
# entry the console renders. No annotations, no entry - undocumented
# options stay engineer-only by design.
{ lib }:
{
  # exportCatalog: modules -> [ { name; type; description; default? } ]
  # Evaluate the module set only for its option declarations.
  exportCatalog = { modules }:
    let
      eval = lib.evalModules {
        modules = modules ++ [{ _module.check = false; }];
      };
      walk = prefix: opts:
        lib.concatLists (lib.mapAttrsToList
          (name: v:
            let path = prefix ++ [ name ]; in
            if lib.isOption v then
              lib.optional (v ? description && v.description != null) {
                name = lib.concatStringsSep "." path;
                type = v.type.description or "unknown";
                description =
                  if lib.isString v.description
                  then v.description
                  else v.description.text or "";
              }
            else if lib.isAttrs v then walk path v
            else [ ])
          opts);
      dawoOpts = (eval.options.dawo or { });
    in
    walk [ ] dawoOpts;
}
