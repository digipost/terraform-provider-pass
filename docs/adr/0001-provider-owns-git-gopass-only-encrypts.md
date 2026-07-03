# Provider owns the git layer; gopass is used only for encryption

The provider was pinned to gopass v1.9.2 (2020) because newer gopass moved its store
packages to `internal/`. Its git handling broke on detached-HEAD checkouts (the
`.terraform/modules` hack used to materialize the store), failing every write with a
refspec error. We upgrade to modern gopass (v1.16+) via its public `pkg/gopass/api`,
but that API deliberately does not expose git sync (`Sync()` is unimplemented) — so the
provider implements clone, fetch, rebase, and push itself by shelling out to `git`,
with gopass reduced to GPG encryption and pass-format file handling.

## Consequences

- The provider clones the password store itself (`repo_url`) into a persistent per-repo
  cache dir, on a real branch — the `.terraform/modules` detached-HEAD hack is obsolete.
- Writes are: flock cache → fetch + rebase onto `origin/<branch>` → commit → push, with
  up to 3 fetch/rebase/push retries on rejection; a same-file conflict fails the
  resource loudly rather than picking a winner.
- Humans use the `pass` CLI against the same repo daily, so the provider constructs
  secret bytes itself (password line + `---\n<yaml>` body) and never lets gopass's
  metadata serialization decide the on-disk layout.
- gopass's `core.autopush`/`core.autosync` must be disabled so its background queue
  never races our git layer.
