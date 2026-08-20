# Create refuses to overwrite a secret that already exists in the store

`pass_password`'s `CreateContext` and `UpdateContext` used to be the same function:
both just wrote the resource's `password`/`data` straight over whatever was at `path`.
That is correct for Update — terraform only calls it for a path it already tracks in
state — but wrong for Create: terraform calls it for a *new* resource, and the git
password store is a shared, git-native store that other teams, tools, or a human `pass`
CLI user may have already written to at the same path, entirely outside terraform's
knowledge. A `pass_password` resource newly added to a config, with a path that
happens to collide with such a secret, would silently destroy it on `terraform apply`
with no diff, warning, or way to notice before it was gone.

## Decision

`CreateContext` (`passPasswordResourceCreate`) first reads `path` from the store. If a
secret is already there, Create fails with an error pointing the operator at
`terraform import` instead of writing. Only when the path is confirmed empty (or the
provider gets the expected "not found" error) does it fall through to the same write
path Update uses.

## Consequences

- Creating a `pass_password` resource at a path that already holds a secret is now a
  hard error instead of a silent overwrite. The error message names the exact
  `terraform import` invocation to adopt the existing secret instead.
- Create costs one extra decrypt-free existence read against the store before writing.
  It reuses the store's normal read path (`withRead`), so it needs no new locking.
- This check is a best-effort guard, not a distributed lock: it reads before the
  write's own fetch/rebase/push cycle, so a secret added at the same path by someone
  else in that narrow window is not caught here. It is still caught — the subsequent
  push's rebase conflicts on the colliding file and fails loudly, consistent with how
  concurrent writes are already handled (see ADR 0001).
- Update and Delete are unaffected: terraform only calls them for a path already in
  state, so the existing unconditional-write / idempotent-delete behavior is still
  correct there.
