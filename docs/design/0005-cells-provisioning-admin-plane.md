# Design 0005: cell provisioning + thin admin plane (ADR 0009 execution)

Status: designed, ready to build

## Problem

ADR 0009 chose instance-per-tenant cells. Today provisioning a cell is
hand work (helmrelease + secrets + overlay repo). 1.0 needs: a runbook
that is mostly `git commit`, and a thin global view over all cells.

## Architecture

### A cell is four artifacts, all declarative

1. **Overlay repo** on the forge: from a template repo
   (sextant-overlay-template: v3 flake + empty fleet.json + catalog
   export wiring). Forgejo supports template repos natively.
2. **Secrets**: one K8s Secret per cell (SEXTANT_* keys) + one forge
   deploy key/token pair (read for devices' comin, write+rings/* force
   for the console).
3. **HelmRelease** in the platform GitOps repo under
   tenants/<org>/sextant.yaml: chart ref + values (host
