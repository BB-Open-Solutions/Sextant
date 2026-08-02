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
reclaimable page cache. A validation request covering two shapes took 38.6s end
to end. The gate's limit is 6Gi (raising it is not free: 12Gi previously
starved the node).

`NIX_SHOW_STATS=1` on one real host says where it goes:

```
gc.heapSize              1275785216   (1.19 GiB live)
gc.totalBytes            1715742144   (1.60 GiB allocated)
gc.cycles                         6
sets.elements              40122066   in 2617862 attribute sets -> 652 MiB
values.number              20573755                             -> 471 MiB
envs.number                 8569234                             -> 164 MiB
nrThunks                   11546706
nrOpUpdates                  715170
nrOpUpdateValuesCopied     28705442
cpuTime                        7.72s  of which gc 3.11s (39%)
```

Two things to read out of that. The memory is the module system's own fixpoint —
2.6 million attribute sets holding 40 million attributes, and 28.7 million
values copied by fewer than three-quarters of a million `//` operations. And the
evaluation itself is about **8s of CPU per shape**, not the 35s a request takes:
the rest is git work, overlay sync and candidate staging.

## The floor that belongs to NixOS

Evaluating one NixOS system means running the module system's fixpoint over
roughly ten thousand options. Measured on one of our own hosts that is a 1.19 GiB
live heap, 1.60 GiB allocated, ~8s of CPU, and about 3 GiB of RSS once the
collector's headroom is counted. The evaluator is single-threaded and holds the
whole value graph, so memory — not CPU — is the binding constraint.

This floor is larger than an earlier draft of this document credited, and it is
not something we get to engineer away. Any design that assumes a system
evaluation can be made cheap is wrong. What a design CAN do is stop paying for
it more often than necessary — which is why the conclusion below is
architectural rather than a list of optimisations.

## The costs that belong to us

Everything above that floor is ours, and so is every multiplication of it.

**We instantiate nixpkgs once per host — but this is a smaller prize than it
looks.** `nix/generator.nix` calls `nixpkgs.lib.nixosSystem { inherit system;
... }` without passing `pkgs`, so each host evaluates the nixpkgs module and
imports nixpkgs itself, even though nixpkgs config is set uniformly by the DAWO
core and nothing in the overlay varies it per host.

An earlier draft of this document claimed that was the bulk of the 3GB. It is
not, and the correction matters more than the original claim. Measured on
synthetic hosts of one shape, peak RSS:

```
hosts    per-host    shared    marginal per host
1          827 MiB    827 MiB
2         1448 MiB   1214 MiB    621 -> 387
4         2686 MiB   1985 MiB    619 -> 385
8              -     3333 MiB           337
```

Sharing removes roughly 38% of the marginal cost. Real, worth taking, not an
order of magnitude. The dominant cost is the module fixpoint, which the stats
above show directly.

Worse, the shape of our batching works against sharing by construction: we
evaluate one representative *per shape*, so every host in a batch is a
different shape with a different module set. Batches are therefore the case
where hosts share the least. Beware of benchmarks — including the one above —
that use hosts of identical shape; they flatter the result.

Sharing is nonetheless safe and worth doing. Both variants were proved to
produce a byte-identical derivation, and the NixOS module forbids only
`nixpkgs.config` alongside an external instance (`nixos/modules/misc/nixpkgs.nix`,
the assertion `opt.pkgs.isDefined -> cfg.config == {}`); overlays still apply
through `appendOverlays`.

**We leave 39% of evaluation time in the garbage collector.** `GC_INITIAL_HEAP_SIZE`
is not set on the gate, so Boehm starts small and grows through six collection
cycles to a 1.19 GiB live heap. Sizing the initial heap to the known working
set is a configuration line, not a design change.

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

**Nix's eval cache does not apply to us at all — so we must do the caching.**
An earlier draft said the gate defeats the cache by putting `fleet.json` in the
flake source. Measured, that is not the mechanism: the cache never engages for
this shape of evaluation. Same host, `cpuTime` from `NIX_SHOW_STATS`:

```
cold, cache directory deleted, clean commit   7.79 s
immediately again, nothing changed            8.08 s
after an unrelated edit elsewhere in fleet.json  7.65 s
```

The middle run changed nothing and cost the same. Nix's flake eval cache covers
shallow output paths; `nixosConfigurations.<host>.config.system.build.toplevel`
is neither shallow nor serialisable, so every evaluation is cold by
construction, clean commit or not.

The consequence is the useful part: memoising verdicts ourselves, keyed by
`(overlay revision, classKey)`, is not one option among several. It is the only
caching available to us.

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

**Sharing one `pkgs` costs a little flexibility.** A host may no longer set
`nixpkgs.config`; overlays still work. Today nothing sets either. The generator
must group by *distinct* nixpkgs settings rather than assume one global
instance, so that an overlay which legitimately needs different settings gets
its own instantiation instead of silently sharing the wrong one.

## What this does not solve

The per-shape floor stays. An organisation with genuinely many distinct shapes
pays for them, and the honest advice is that shapes are the thing to keep few —
a new hardware class costs; a thousand more laptops do not.

Nothing here makes a single evaluation much faster. That question was put to
the test rather than assumed: the first draft blamed per-host nixpkgs
instantiation, and the measurement cut it down to ~38% of the marginal cost,
with the module fixpoint accounting for the rest. GC tuning should take a bite
out of the time, not the memory. Treat any further "make the evaluation cheap"
proposal the same way — measure it before writing it down, and correct the
document when the number disagrees.

A residual risk worth naming: the synthetic benchmark above used hosts of a
single shape, while production batches are one host per shape by construction.
The 38% is therefore an upper bound on what sharing buys us in practice. It has
not been measured across genuinely different shapes, because by the time the
question arose the fleet was down to one device.

## Order of work

1. Set `GC_INITIAL_HEAP_SIZE` on the gate. One environment variable against 39%
   of evaluation time. Measure the before and after; do not assume the whole
   39% is recoverable.
2. Type-check saved settings against `catalog.json` (ADR 0005). This is what
   removes the full evaluation from the path an editor actually waits on, and
   it depends on nothing else here.
3. Share one nixpkgs instantiation across hosts (#41). Worth ~38% of the
   marginal per-host cost, and it needs a change to the DAWO core (the
   `nixpkgs.config` it sets from a NixOS module must stand down when a caller
   supplies its own instance) — so: fork and PR, never a direct push.
4. Re-derive `chunkSize` from measurement after 3, and stop treating small
   chunks as safety: each chunk re-pays the fixed cost.
5. Move the evaluation off the write path and key verdicts by
   `(overlay revision, classKey)` (#40). Since nix caches nothing for us, this
   is the whole of our caching strategy rather than a supplement to it.

An earlier draft had a sixth step — split `fleet.json` so nix's eval cache
survives unrelated edits. The measurement above removed its justification: the
cache never engages, so there is nothing to preserve. `classKey` is already
per-shape and precise, which is what that step was really after.

Steps 1–4 are optimisations and each stands alone; together they buy a constant
factor, not a different curve. Step 5 is the one that changes the curve, and
the measurements in this document are the argument for doing it rather than
continuing to tune: the per-shape cost is a floor, so the only lasting win is
paying it fewer times.

## A note on measuring this

Two methods gave confidently wrong answers during the work above, both caught
only by cross-checking:

- `time -v`'s maximum resident set size does not capture `nix eval` on a flake.
  It reported 44 MiB and 0.13s for an evaluation that `NIX_SHOW_STATS` shows
  costs 8s of CPU and 7.2 million function calls.
- `gc.heapSize` is elastic, not a requirement. The same evaluation reports
  1.28 GiB in the memory-limited gate pod and 3.2 GiB on a laptop with room to
  spare, because Boehm sizes the heap to what is available. Compare
  `gc.totalBytes` and cgroup `anon`, and treat `heapSize` as a decision the
  collector made rather than a number we need.

Use `NIX_SHOW_STATS=1` and the cgroup. Anything else here has already lied
once.
