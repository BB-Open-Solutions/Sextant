# resolve.nix - the nix twin of internal/domain/fleet (resolve.go, chain.go,
# filter.go). Overlays import THIS file from the Sextant flake, so the Go
# resolver and the generator can never drift: both sides of the contract
# live in one repo and the parity harness (parity_test.go) proves them
# byte-equal on shared fixtures.
#
# Precedence (identical to the Go resolver):
#   enforced -> the MOST GENERAL enforcing contributor wins (mkForce side);
#   default  -> the MOST SPECIFIC contributor wins (mkDefault side);
#   ties: inline scope settings beat policy contributions, then higher
#   assignment priority, then earlier assignment order.
#
# Input: fleet = the parsed fleet.json (v3), tag = device tag.
# Output: { <key> = { value; source = { scope; policy? }; enforced; }; }
# Pure builtins only: no nixpkgs dependency, evaluates anywhere.
{ }:
let
  inherit (builtins) attrNames concatLists concatMap elem filter foldl'
    genList hasAttr length listToAttrs substring stringLength;

  # Minimal lib replacements (pure builtins).
  optional = cond: x: if cond then [ x ] else [ ];
  unique = foldl' (acc: x: if elem x acc then acc else acc ++ [ x ]) [ ];
  any = f: xs: foldl' (a: x: a || f x) false xs;
  all = f: xs: foldl' (a: x: a && f x) true xs;
  imap0 = f: xs: genList (i: f i (builtins.elemAt xs i)) (length xs);
  genAttrs = names: f: listToAttrs (map (n: { name = n; value = f n; }) names);

  # groupAncestry: root -> ... -> g, cycle/dangling guarded (fleet.go).
  groupAncestry = fleet: g:
    let
      walk = cur: seen: chain:
        if cur == "" || elem cur seen || !(hasAttr cur (fleet.groups or { }))
        then chain
        else walk ((fleet.groups.${cur}.parent or "")) (seen ++ [ cur ]) ([ cur ] ++ chain);
    in
    walk g [ ] [ ];

  hasPrefix = pre: s:
    stringLength s >= stringLength pre && substring 0 (stringLength pre) s == pre;

  # scopePositions (chain.go): org=0, each device group's ancestry in device
  # order (first-seen position wins), device last.
  scopePositions = fleet: tag:
    let
      dev = fleet.devices.${tag} or { };
      groups = dev.groups or [ ];
      step = acc: g:
        foldl'
          (a: anc:
            if hasAttr "group:${anc}" a.pos then a
            else {
              pos = a.pos // { "group:${anc}" = a.next; };
              next = a.next + 1;
            })
          acc
          (groupAncestry fleet g);
      folded = foldl' step { pos = { "org" = 0; }; next = 1; } groups;
    in
    folded.pos // { device = folded.next; };

  # matchesRule / matchesFilter (filter.go): closed vocabulary, fail-closed.
  matchesRule = fleet: tag: r:
    let
      dev = fleet.devices.${tag} or null;
      op = r.op or "";
      val = r.value or "";
      vals = r.values or [ ];
      groupsExpanded =
        unique (concatMap (g: groupAncestry fleet g) (dev.groups or [ ]));
      got =
        if (r.attr or "") == "tag" then tag
        else if r.attr == "class" then dev.class or ""
        else if r.attr == "hardware" then dev.hardware or ""
        else if r.attr == "assignedUser" then dev.assignedUser or ""
        else if hasPrefix "label:" (r.attr or "")
        then (dev.labels or { }).${substring 6 (stringLength r.attr - 6) r.attr} or ""
        else null;
    in
    if dev == null then false
    else if (r.attr or "") == "group" then
      (if op == "eq" then elem val groupsExpanded
      else if op == "ne" then !(elem val groupsExpanded)
      else if op == "prefix" then any (g: hasPrefix val g) groupsExpanded
      else if op == "in" then any (v: elem v groupsExpanded) vals
      else false)
    else if got == null then false
    else if op == "eq" then got == val
    else if op == "ne" then got != val
    else if op == "prefix" then got != "" && hasPrefix val got
    else if op == "in" then elem got vals
    else false;

  matchesFilter = fleet: tag: fl:
    let
      rules = fl.rules or [ ];
      mode = if (fl.match or "all") == "" then "all" else fl.match or "all";
      results = map (r: matchesRule fleet tag r) rules;
    in
    if rules == [ ] then false
    else if mode == "any" then any (x: x) results
    else all (x: x) results;

  # assignmentPosition (chain.go): where a target sits on the chain.
  assignmentPosition = pos: target: tag:
    if target == "org" then { spec = pos."org"; applies = true; }
    else if hasPrefix "group:" target then
      (if hasAttr target pos
      then { spec = pos.${target}; applies = true; }
      else { spec = 0; applies = false; })
    else if hasPrefix "device:" target then
      (if substring 7 (stringLength target - 7) target == tag
      then { spec = pos.device; applies = true; }
      else { spec = 0; applies = false; })
    else { spec = 0; applies = false; };

  displayScope = target:
    if hasPrefix "device:" target then "device" else target;

  # chainFor (chain.go): inline scope contributors + applicable policies.
  chainFor = fleet: tag:
    let
      pos = scopePositions fleet tag;
      dev = fleet.devices.${tag} or { };
      inlineOf = spec: scopeName: s:
        optional
          ((s.settings or { }) != { } || (s.enforced or [ ]) != [ ])
          {
            inherit spec;
            inline = true;
            prio = 0;
            order = -1;
            source = { scope = scopeName; };
            settings = s.settings or { };
            enforced = s.enforced or [ ];
          };
      orgC = inlineOf pos."org" "org" (fleet.org or { });
      groupCs = concatMap
        (ref:
          if hasPrefix "group:" ref
          then
            inlineOf pos.${ref} ref
              (fleet.groups.${substring 6 (stringLength ref - 6) ref} or { })
          else [ ])
        (attrNames pos);
      devC = inlineOf pos.device "device" dev;
      policyCs = concatLists (imap0
        (i: a:
          let
            p = (fleet.policies or { }).${a.policy or "__missing"} or null;
            ap = assignmentPosition pos (a.target or "") tag;
            fl = (fleet.filters or { }).${a.filter or "__missing"} or null;
            filterOK =
              if (a.filter or "") == "" then true
              else fl != null && matchesFilter fleet tag fl;
          in
          if p == null || !ap.applies || !filterOK then [ ]
          else [{
            spec = ap.spec;
            inline = false;
            prio = a.priority or 0;
            order = i;
            source = { scope = displayScope a.target; policy = a.policy; };
            settings = p.settings or { };
            enforced = p.enforced or [ ];
          }])
        (fleet.assignments or [ ]));
    in
    orgC ++ groupCs ++ devC ++ policyCs;

  # better (resolve.go): a beats b for the wanted direction.
  better = wantGeneral: a: b:
    if a.spec != b.spec
    then (if wantGeneral then a.spec < b.spec else a.spec > b.spec)
    else if a.inline != b.inline then a.inline
    else if a.prio != b.prio then a.prio > b.prio
    else a.order < b.order;

  pick = wantGeneral: cs:
    foldl' (best: c: if best == null || better wantGeneral c best then c else best) null cs;

  resolveKey = chain: key:
    let
      having = filter (c: hasAttr key c.settings) chain;
      enforcers = filter (c: elem key c.enforced) having;
      enf = pick true enforcers;
      def = pick false having;
    in
    if enf != null
    then { value = enf.settings.${key}; source = enf.source; enforced = true; }
    else { value = def.settings.${key}; source = def.source; enforced = false; };
in
{
  # resolve: every effective setting for a device, with provenance.
  resolve = fleet: tag:
    let
      chain = chainFor fleet tag;
      keys = unique (concatMap (c: attrNames c.settings) chain);
    in
    genAttrs keys (k: resolveKey chain k);

  # Exposed for the generator and tests.
  inherit groupAncestry chainFor;
}
