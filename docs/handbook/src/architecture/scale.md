# Scaling to 10,000+ devices

Sextant targets fleets of 10,000+ devices per organisation. This chapter is the
reference architecture for that scale: which parts grow with the fleet, which
do not, and the numbers behind each decision. The pilot deployment is this
exact architecture at N=1 per tier - scaling out means adding workers to a
tier, not redesigning.

## Four planes, scaled independently

| Plane | Runs | Scales with |
|---|---|---|
| **Control** | Console, Postgres, git (Forgejo), IdP | Barely - operators, not devices |
| **Eval (gate)** | Pool of gate-runner workers, each memory-bounded | Worker count = wall-clock for a full validation |
| **Build** | Nix build workers pushing to a signed binary cache | Number of distinct config shapes, not devices |
| **Cache / delivery** | S3-compatible object store (MinIO / Garage) + signing keys | Bandwidth; rollout rings stagger the pulls |

Every component is FOSS and self-hosted in the customer's datacenter or a
sovereign cloud. No proprietary or extra-territorial dependency.

The check-in path (observed plane) is not the bottleneck: 10,000 devices at one
check-in per 60s is ~170 writes/s, comfortably inside a single Postgres with
batched upserts and a partitioned status table.

## Why devices fetch instead of build

comin converges each device by evaluating the overlay and rebuilding locally.
At pilot scale that is fine; at 10,000 devices it means the same derivations
are compiled 10,000 times on weak edge hardware, and a rollout's wall-clock is
the slowest device's build.

At scale the pipeline builds once, centrally, per ring - **build-before-
promote**: the delivery pipeline realises ring N's closures on the build
workers, pushes them to the signed binary cache, and only then flips the ring
branch. Devices substitute (download) instead of compiling. Ring ordering
naturally staggers cache load.

This is the largest gap between the pilot and the enterprise posture, and the
first slice to build.

## Why the gate is batched, and what the numbers say

The gate proves a change evaluates before it reaches git (see
[Safe writes](../concepts/safe-writes.md)). Forcing every host's toplevel in a
single nix process scales memory with the fleet - a whole-fleet evaluation
OOM-killed the runner well before 100 hosts. The gate therefore evaluates in
memory-bounded batches (`chunkSize`, default 50, deployed at 12): peak memory
is the batch, not the fleet, and every affected host is still evaluated.

Batches are independent, which makes them the unit of horizontal scaling.
Org-wide validation of 10,000 hosts at chunk size 12 and ~45s per chunk:

| Strategy | Wall-clock |
|---|---|
| 1 worker, sequential (today) | ~10 hours |
| 16 workers, parallel chunks | ~40 minutes |
| Equivalence-class sampling | minutes |

The conclusions fall out of the table:

- **A scoped change stays interactive.** Its blast radius is a handful of
  hosts; the gate evaluates only those (`AffectedHosts`). Metadata-only changes
  (groups, access, governance) skip the evaluation entirely - they cannot
  alter any device's build.
- **A genuinely org-wide change is validated asynchronously.** It flows through
  the delivery pipeline, where chunk-parallel workers evaluate the full fleet
  before the first ring promotes. Nobody waits 10 hours at a save button.
- **Interactive org-wide feedback uses equivalence classes.** 10,000 devices
  produced by the same generator collapse to dozens of distinct config shapes
  (hardware profile x settings signature). An option or type error fails every
  host in its class, so evaluating one representative per class catches it in
  minutes. The full per-host evaluation still runs in the pipeline - sampling
  narrows feedback latency, never the guarantee. The class partitioner is
  security-critical code and is treated accordingly (tested exhaustively,
  reviewed as part of the gate).

## Availability

The gate is fail-closed: if no gate worker is reachable, config writes are
refused rather than committed unvalidated. Gate workers therefore live in the
control plane's availability domain (at least two at scale), not on hardware
that may be powered off. Build workers may come and go - a missing build
worker delays a rollout, never corrupts one.

## Dogfooding the infra

Eval and build workers are themselves NixOS machines - so they are enrolled as
Sextant devices in an `infra` group and managed declaratively by the product
they serve. Scaling the build plane is enrolling another worker.

## Roadmap to this posture

1. **Build-before-promote** - pipeline builds ring closures to the signed
   cache before the ring branch flips; devices substitute.
2. **Chunk-parallel gate** - a queue distributes batches over a worker pool;
   the batching primitive already exists.
3. **Equivalence-class sampling** for interactive org-wide feedback.
4. **Infra group** - build/eval workers enrolled and managed by Sextant itself.
