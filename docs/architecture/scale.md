# Scale: what a fleet change costs, and who pays for it

Status: Accepted (2026-08-02)

Four places in the code already cite this file — `internal/domain/fleet/classes.go`,
`internal/domain/fleet/classes_bench_test.go`, `internal/app/config.go` and
`internal/http/web/devices_page.go`. It was never written. What follows is the
argument those citations were pointing at, plus the numbers that were missing
when they were made.

## Context

The requirement Bram set is that this scales: many devices, many editors, and
eventually many organisations on one console. On 2026-08-01 it did not. A fleet
of **five devices** OOM-killed the validation gate three times in an hour, and
because every config commit is validated before it is written, the whole
control plane stopped with it — no device edit, no rollout pin, nothing. The
only thing an operator saw was `EOF`.

The forcing question (Bram, 2026-08-02): *if one core update lands, does every
profile have to be re-evaluated, at 3GB each?*

The honest answer is that the re-evaluation is real and unavoidable, and the
3GB is mostly ours.

## The measurements

Taken in the running gate pod, sampling the cgroup's anonymous memory (not
`kubectl top`, which reports working set and understates this by an order of
magnitude) while a real evaluation ran:

```
1 host   3001 MiB peak anon
2 hosts  at the 6Gi limit - observed both one success and repeated OOM kills
```

`file` was 430 MiB against `anon` of 5.08 GiB, so this is evaluator heap, not
reclaimable page cache. A single validation of two shapes took 38.6s end to
end. The gate's limit is 6Gi (raising it is not free: 12Gi previously starved
the node).

Marginal cost ≈ fixed cost. That is the signature of hosts sharing nothing,
and it is the single most important number in this document.

## The floor that belongs to NixOS

Evaluating one NixOS system means running the module system's fixpoint over
roughly ten thousand options. For a realistic workplace configuration that is
on the order of 1–2 GB and tens of seconds. The evaluator is single-threaded
and holds the whole value graph, so memory — not CPU — is the binding
constraint.

We do not get to remove this. Any design that assumes a system evaluation can
be made cheap is wrong. What a design CAN do is stop paying for it more often
than necessary.

## The costs that belong to us

Everything above that floor is ours, and so is every multiplication of it.

**We instantiate nixpkgs once per host.** `nix/generator.nix` calls
`nixpkgs.lib.nixosSystem { inherit system; ... }` without passing `pkgs`, so
each host evaluates the nixpkgs module and imports nixpkgs itself. Every host
pays in full for an instantiation that is *identical* to every other host's:
nixpkgs config is set uniformly by the DAWO core and nothing in the overlay
varies it per host. This is the bulk of the 3GB.

**We then chunk to work around it.** `chunkSize` was 12 — a number derived from
"17 hosts OOM'd at 4Gi, so 12 fits under 6Gi", which nobody checked against a
running pod and which was wrong by more than an order of magnitude. It is now
1. But chunking makes the *total* worse: each chunk re-pays the fixed cost. We
have been multiplying the very thing we should be sharing. Once hosts share an
instantiation the correct move is the opposite — fewer, larger `nix`
invocations, because nix deduplicates common work across targets in one
process.

**We validate synchronously, in the write path.** An edit blocks on the
evaluation before it is committed. The console serialises the whole change flow
(one git working tree) and the gate has a single evaluation slot behind an
admission queue of four. Concurrency is therefore 1 by construction, end to
end: the second editor waits for the first, the fifth is refused. For the SaaS
direction this is the wall — one slot shared by every tenant.

**We defeat nix's own eval cache.** The gate stages each candidate as a clean
commit precisely so the eval cache applies. But `fleet.json` is a single blob
inside the flake source, so *any* fleet edit changes the source hash and every
shape re-evaluates cold. The cache helps only for repeated evaluations of the
identical candidate, which is not a case that occurs.

**We use a full system evaluation to answer questions a type check would
answer.** `catalog.json` publishes every `dawo.*` option with its type
(ADR 0005), and nothing validates a saved setting against it. The
overwhelming majority of bad edits are type and enumeration errors. They cost
microseconds to catch. We spend 3GB and 35 seconds on them.

**We do the same computation twice.** The interactive gate evaluates a shape to
prove it is sound. Later, build-before-promote realises that same shape's
toplevel before a ring branch moves. These are the same question asked twice,
once wastefully.

## What already scales, and must be kept

The sampling is right and it is the reason this is fixable rather than fatal.

`Fleet.EquivalenceClasses` partitions devices into configuration *shapes* — same
hardware profile, same class, same resolved settings, same app sets — and the
gate evaluates one representative per shape. **Cost tracks shapes, not
devices.** A thousand identical laptops cost the same as one. The partitioner
keys on resolved state rather than raw scope data and is documented to err
toward more classes rather than fewer, which is the property that makes the
sampling safe; `BenchmarkRepresentatives10k` holds it to that at size.

Also load-bearing, and easy to overlook: **`main` is not what devices run.**
Devices follow ring pins. An unvalidated commit on `main` is inert until a
rollout promotes it, and promotion already proves every member's toplevel.
This is what makes it legitimate to move validation off the write path without
weakening any guarantee.

## Decision: three layers, each paying its own honest cost

**1. Saving is a type check.** A setting written from the console is validated
against `catalog.json` — key exists, type matches, enumeration member is real —
and committed. Synchronous, microseconds, no nix. This is what an editor
waits for, and it catches most of what an editor gets wrong.

**2. Proving is a build, done once, shared by everyone.** The full evaluation
moves out of the write path onto a builder, keyed by content. A verdict is a
function of `(overlay revision, classKey)` and nothing else — `classKey` is
already a hash of exactly the build inputs, and the partitioner's bias toward
more classes is what makes that key trustworthy. Two organisations running the
same hardware with the same resolved settings produce the same key and share
the answer, so the tenth tenant is cheaper than the first.

Crucially this is the *same* job as build-before-promote, not an extra one. It
produces the closures devices substitute. Validation stops being overhead and
becomes the release build, arriving early.

**3. Rolling out references what is already proven.** A ring promotes to a
revision whose shapes carry verdicts and whose closures are in the cache. The
promotion itself does no evaluation.

The console then produces nothing. It writes data, computes `classKey`s and
looks up verdicts — O(1) per edit, independent of fleet size, editor count and
tenant count. Throughput becomes the number of builders, which is horizontal.

## Consequences

**A core update re-proves every shape, and that is correct.** The core changed;
no cache key can honestly survive it. What changes is the shape of the bill:
one invocation over all shapes instead of one per shape, once globally instead
of once per tenant, on a builder instead of in the write path, and producing
the closures the fleet was going to download anyway. It becomes a build-farm
job — which is what it always was.

**Editors stop blocking each other.** With the eval off the write path there is
no shared evaluation slot to queue for.

**A bad config can reach `main` unproven.** This is the deliberate trade, and it
is safe only because ring pins gate what devices actually run. If that ever
stops being true this decision must be revisited first.

**Sharing one `pkgs` costs flexibility.** A host may no longer set
`nixpkgs.config` or `nixpkgs.overlays`. Today nothing does. The generator must
group by *distinct* nixpkgs settings rather than assume one global instance, so
that an overlay which legitimately needs different settings gets its own
instantiation instead of silently sharing the wrong one.

## What this does not solve

The per-shape floor stays. An organisation with genuinely many distinct shapes
pays for them, and the honest advice is that shapes are the thing to keep few —
a new hardware class costs; a thousand more laptops do not.

Nothing here makes a single evaluation faster. If the 3GB does not drop once
hosts share an instantiation, the hypothesis in this document is wrong and the
memory is going somewhere nobody has looked yet. Measure before believing it.

## Order of work

1. Share one nixpkgs instantiation across hosts (#41). One argument in
   `mkFleet`, verified by re-running the measurement above at 1, 2, 4 and 8
   hosts. This removes the largest constant before any architectural work
   starts, and it tells us whether the rest of this document rests on a
   correct diagnosis.
2. Raise `chunkSize` from the new measurement, and stop treating small chunks
   as safety.
3. Type-check saved settings against `catalog.json`.
4. Move the evaluation off the write path and key verdicts by
   `(overlay revision, classKey)` (#40).
5. Split `fleet.json` so an edit invalidates the shapes it touches rather than
   all of them, letting nix's own eval cache do the memoisation.

Steps 1–3 are independent and each stands on its own. Step 4 is the
architectural change and should not start before step 1 has been measured.
