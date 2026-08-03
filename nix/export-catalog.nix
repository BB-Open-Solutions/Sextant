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
    # usbControl is driven by an overlay-provided surface (dawo.usbDevices),
    # not set directly. Two reasons, both temporary: the core declares its
    # allowlist as `options.allowlist` inside the option set, so the key reads
    # "usbControl.options.allowlist", and its label annotations do not survive
    # evaluation, so both entries would render as raw dotted paths. Exporting
    # them alongside the clean pair would give an operator four keys for two
    # settings, two of them unlabelled - and the unlabelled dangerous one is
    # exactly the one nobody should reach for by accident. Remove this line
    # once the core names and labels them properly.
    [ "usbControl" ]
  ];
  isManaged = path:
    lib.any (p: lib.take (lib.length p) path == p) managedPrefixes;
  riskClass = opt:
    if (opt.riskClass or "") != "" then { riskClass = opt.riskClass; } else { };
  # secret: an option annotated `// { secret = true; }` renders in the console
  # as a secret-ref picker (the value is a secret name, never the material).
  secret = opt:
    if (opt.secret or false) then { secret = true; } else { };
  # label: an option annotated `// { label = "LUKS mapper"; }` shows that
  # human name in the console instead of the raw dotted path (which stays
  # visible as the technical identity - Name remains the API key).
  label = opt:
    if (opt.label or "") != "" then { label = opt.label; } else { };
  walk = prefix: opts:
    lib.concatLists (lib.mapAttrsToList
      (name: v:
        let path = prefix ++ [ name ]; in
        # Managed plumbing (autoUpdate, secureboot.pkiBundle, ...) is wired by
        # Sextant, never an operator setting - skip the whole subtree.
        if isManaged path then [ ]
        else if lib.isOption v then
          lib.optional (v ? description && v.description != null) ({
            name = lib.concatStringsSep "." path;
            type = v.type.description or "unknown";
            description =
              if lib.isString v.description
              then v.description
              else v.description.text or "";
          } // plainDefault v // riskClass v // secret v // label v)
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

  # exportCatalogFromClassOptions: { <class> = <evaluated host options>; ... }
  # -> catalog entries tagged with the classes whose IMAGE defines them.
  #
  # WHY THIS EXISTS. Exporting from a single host publishes that host's
  # options as if every device had them. The console's CatalogEntry.AppliesTo
  # and the generator both promise the opposite - a workplace-only option set
  # at a scope covering headless machines should configure the laptops and
  # visibly skip the servers - and neither could keep that promise, because
  # the data it needs was never in the file. Setting a laptop option at org
  # scope failed the evaluation of every station in the blast radius, and the
  # console refused a change the operator had every reason to expect to work.
  #
  # An entry every class defines carries NO classes list. That is the
  # universal case and AppliesTo already reads an empty list that way, so the
  # common option stays a plain row rather than one tagged with every class
  # in the organisation.
  exportCatalogFromClassOptions = byClass:
    let
      classes = lib.attrNames byClass;
      # class -> its entries, keyed by option name.
      # walk directly rather than through the sibling attribute: these are
      # members of the returned set, not let-bindings, so they cannot see
      # each other.
      perClass = lib.mapAttrs (_: opts:
        lib.listToAttrs (map (e: lib.nameValuePair e.name e)
          (walk [ ] (opts.dawo or { })))) byClass;
      names = lib.unique (lib.concatMap lib.attrNames (lib.attrValues perClass));
      definedIn = name:
        lib.filter (c: perClass.${c} ? ${name}) classes;
      # The entry itself comes from the first class that defines it, in
      # attrName order, so the export is deterministic. Description and type
      # come from the same option in every image, so the choice is arbitrary
      # only in the sense that it cannot matter.
      entryFor = name:
        let owners = definedIn name; in
        perClass.${lib.head owners}.${name}
          // (if lib.length owners == lib.length classes
              then { }
              else { classes = owners; });
    in
    map entryFor (lib.sort (a: b: a < b) names);

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
