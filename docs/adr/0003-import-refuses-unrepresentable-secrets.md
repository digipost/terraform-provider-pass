# Import refuses secrets the resource cannot faithfully represent

ADR 0002 made `pass_password`'s Create refuse to overwrite an untracked secret
and point the operator at `terraform import` instead — so import is the
sanctioned path by which human-written secrets enter terraform management. A
plain passthrough importer, however, silently destroys shared-store content in
two ways:

- The resource models only `path`, `password` and a flat string `data` map.
  A human-written secret with free-form note lines, CRLF line endings, or
  YAML values the map cannot hold (nested structures, floats, booleans, empty
  values) imports "successfully" with those parts missing or coerced
  (`1.10` → `"1.1"`, `yes` → `"true"`, nesting → Go debug syntax), the plan
  looks clean, and the next apply rewrites the store file without them.
- gopass normalizes secret names on read (leading/trailing slashes are
  trimmed, the `.gpg` suffix is re-appended), so a non-canonical import ID
  like `/team/secret` or `team/secret.gpg` also imports "successfully" — but
  the state then holds a `path` that differs from the configuration's, and
  `path` is ForceNew, so the next apply deletes the real secret and recreates
  it from config. A one-character import typo destroys the secret import
  exists to protect.

## Decision

The importer is a custom `StateContext` (`passPasswordResourceImport`) that
refuses instead of adopting when either hazard is present:

- The import ID must already be the canonical extension-less store path
  (`canonicalStorePath`); anything gopass would silently normalize is refused
  with the corrected path named. Refusing rather than auto-fixing keeps the
  invariant that the resource id and the `path` attribute are exactly the
  string the operator typed, which the configuration must repeat.
- The secret's plaintext must pass `checkSecretRepresentable`: a password
  line, then either nothing or one `---`-delimited YAML map whose keys are
  strings and whose values are strings or integers. Anything else — free-form
  body text, CRLF, a bare YAML document, multiple documents, non-map YAML,
  lossy value types — is refused with a description of what would be lost.

SDKv2 importers can only return errors, not warnings, so refusal is the only
available signal; there is no "import anyway with a warning" middle ground.

## Consequences

- The resource id being the store path verbatim is now user-facing API (typed
  into `terraform import`), not just an internal convention of `d.SetId`.
- Import of representable secrets still permits representation-only rewrites
  on the first subsequent write: key reordering, value quoting, integer base
  normalization, dropped comments and duplicate keys. These preserve every
  key and value and are documented in the resource's Import docs.
- A secret whose store path genuinely ends in `.gpg` or `.txt` cannot be
  imported (the ID is indistinguishable from a filename mistake). Such paths
  can still be created and managed by terraform; they just cannot be adopted.
- Import costs one extra store read before terraform's own refresh — the same
  shape as ADR 0002's existence check.
- Known remaining gap, out of scope here: a human adding free-form content to
  an *already-managed* secret is still silently truncated by the next apply,
  because Read cannot represent the body either. Read can emit warnings
  (unlike importers), so a future change could surface
  `checkSecretRepresentable` failures there as a `diag.Warning`.
