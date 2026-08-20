---
page_title: "Upgrading to 2.0 - pass Provider"
subcategory: ""
description: |-
  Migrating from the 1.x pass provider (e.g. 1.7.7) to 2.0.
---

# Upgrading to 2.0

2.0 changes how the provider talks to git. In 1.x, `gopass` (pinned to v1.9.2)
handled cloning, fetching and pushing internally. From 2.0 on, the provider
implements clone/fetch/rebase/push itself and uses `gopass` only for GPG
encryption and pass-format file handling — see
[ADR 0001](https://github.com/digipost/terraform-provider-pass/blob/main/docs/adr/0001-provider-owns-git-gopass-only-encrypts.md)
for the full rationale.

This guide walks through what changes and what to do about it. Read the
"Breaking changes" section fully before upgrading a production configuration.

## Before you start

- Note which mode your current config uses: `store_dir` pointed directly at a
  checkout, or the common 1.x workaround of a Terraform module checkout (see
  below). 2.0 handles these differently.
- Make sure whoever/whatever runs `terraform apply` (including CI) has a git
  identity configured (`git config user.name` / `user.email`). 2.0 requires
  one for every write; 1.x did not always need it depending on gopass's
  internal git handling.
- Have SSH/HTTPS access to the password store's git remote ready if you plan
  to switch to `repo_url`.

## Breaking changes

### 1. The `.terraform/modules` workaround is rejected, not silently broken

1.x could not clone the store itself, so a common workaround was materializing
it through a Terraform module checkout:

```terraform
# 1.x workaround — remove this
module "dppasswords" {
  source = "git@github.com:example-org/passwords.git"
}

provider "pass" {
  store_dir = ".terraform/modules/dppasswords"
}
```

Module checkouts are on a detached HEAD. In 1.x this half-worked and then
failed *mid-apply* with a git refspec error
(`the destination you provided is not a full refname`). In 2.0 this fails
immediately at *configure* time instead, with an error pointing you at
`repo_url`.

**Fix:** delete the module block and use the new `repo_url` attribute — the
provider now clones and manages the checkout itself:

```terraform
# 2.0
provider "pass" {
  repo_url = "git@github.com:example-org/passwords.git"
}
```

### 2. `store_dir` now validates more strictly at configure time

With the default `refresh_store = true`, `store_dir` must point at a real git
clone with an `origin` remote, checked out on a branch (not detached HEAD).
If your `store_dir` is:

- **A plain directory with no `.git`** (or a repo with no `origin` remote):
  set `refresh_store = false` — the provider will read/write files directly
  without trying to sync.
- **A detached-HEAD checkout:** see #1 above, switch to `repo_url`.
- **A normal branch checkout with an `origin` remote:** no change needed.

### 3. Provider attribute changes: `repo_url` and `store_dir` are mutually exclusive

Exactly one of `repo_url` or `store_dir` must be set — configuring both, or
neither, now fails at configure time. `store_dir` still falls back to the
`$PASSWORD_STORE_DIR` environment variable when unset.

### 4. Data source attributes are now `sensitive`

`password`, `data`, `body` and `full` on the `pass_password` data source are
now marked `Sensitive`. Any output referencing them must be declared
`sensitive = true`, or `terraform plan`/`apply` will error:

```terraform
output "db_password" {
  value     = data.pass_password.db.password
  sensitive = true # now required
}
```

### 5. Writes require a git identity

Every create/update/delete now creates a real git commit
(`terraform-provider-pass: write <path>` / `terraform-provider-pass: delete
<path>`), authored using the git config of whoever runs `terraform apply`.
If that identity isn't configured, writes fail with git's "Please tell me who
you are" error. **Configure `user.name`/`user.email` in CI jobs** — this is
easy to miss since some 1.x setups didn't require it.

## Behavior changes worth knowing about (not breaking, but new)

- **The provider pushes for you now.** Every write does
  fetch → rebase onto `origin/<branch>` → commit → push, retrying a rejected
  push up to 3 times. You no longer need any external step to push the
  store's remote after `terraform apply` — if your pipeline had one, it's now
  redundant (harmless, but you can remove it).
- **Concurrent writes to the same secret fail loudly instead of silently
  picking a winner.** If two `terraform apply` runs (or a run and a manual
  `pass`/`gopass` edit) touch the same secret at the same time, the rebase
  conflicts and the run fails with an error telling you to inspect the store
  and re-run — it never auto-resolves the conflict for you.
- **Creating a `pass_password` resource at a path that already has an
  untracked secret now fails** instead of silently overwriting it. If you add
  a new `pass_password` resource for a secret a human (or another tool)
  already created directly in the store, `terraform apply` errors and names
  the `terraform import` command to adopt it instead. See
  [ADR 0002](https://github.com/digipost/terraform-provider-pass/blob/main/docs/adr/0002-create-refuses-to-overwrite-untracked-secrets.md).
  **After upgrading** (import support is new in 2.0 — running this on 1.x
  fails with "resource doesn't support import"), if you're about to add
  `pass_password` resources for secrets that already exist in the store
  outside terraform, import them before applying:

  ```sh
  terraform import pass_password.example some/existing/path
  ```
  
- **A secret deleted from the store outside terraform is handled
  gracefully.** Reading it clears it from state (terraform plans a
  recreation) instead of failing, and destroying an already-deleted secret
  succeeds instead of erroring.
- **New optional `branch` attribute** (with `repo_url`): pick which branch of
  the password store to read and write. Defaults to the remote's HEAD branch.

## Step-by-step migration

1. Bump the provider version constraint in `required_providers`.
2. If you use the module-checkout workaround (#1 above), delete the module
   block and switch to `repo_url`.
3. If you use plain `store_dir`, check it satisfies #2 above; set
   `refresh_store = false` if it's not a synced git clone.
4. Add `sensitive = true` to any output that references
   `data.pass_password.*` attributes.
5. Confirm a git identity is configured wherever `terraform apply` runs,
   especially CI.
6. If you're about to add new `pass_password` resources for secrets that
   already exist in the store (created by a human or another tool), import
   them first instead of letting `terraform apply` try to create them.
   Do this after the provider upgrade — 1.x cannot import.
7. Run `terraform plan` and review carefully before `apply` — the plan
   should show no unexpected changes to existing resources; the on-disk
   secret format is unchanged and byte-compatible with what 1.x (and the
   `pass` CLI) wrote.

## Getting help

If a run fails during migration, the error messages are written to name the
exact next step (e.g. which command to run, which attribute to change). If
something is still unclear, check the referenced ADRs
([0001](https://github.com/digipost/terraform-provider-pass/blob/main/docs/adr/0001-provider-owns-git-gopass-only-encrypts.md),
[0002](https://github.com/digipost/terraform-provider-pass/blob/main/docs/adr/0002-create-refuses-to-overwrite-untracked-secrets.md),
[0003](https://github.com/digipost/terraform-provider-pass/blob/main/docs/adr/0003-import-refuses-unrepresentable-secrets.md))
or open an issue.
