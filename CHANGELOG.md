## 2.0.0 (Unreleased)

The provider now owns the git layer itself; gopass is only used for GPG
encryption and pass-format file handling. See
`docs/adr/0001-provider-owns-git-gopass-only-encrypts.md` for the rationale.

BREAKING CHANGES:

* New provider attribute `repo_url`: the provider clones the password store
  into a persistent per-repository cache (`~/.cache/terraform-provider-pass/`),
  refreshes it on configure, and pushes every write. Exactly one of `repo_url`
  and `store_dir` must be set — they are mutually exclusive.
* `store_dir` pointing at a git checkout on a detached HEAD (e.g. a
  `.terraform/modules/...` module checkout) now fails at configure time with
  an error pointing to `repo_url`. Previously this half-worked and then failed
  mid-apply with a git refspec error.
* `store_dir` pointing at a non-git directory (or a repo without an `origin`
  remote) with the default `refresh_store = true` now fails at configure time;
  set `refresh_store = false` for purely local stores.
* The data source attributes `password`, `data`, `body` and `full` are now
  marked sensitive. Outputs referencing them must be declared `sensitive = true`.
* Writes require a git identity (`user.name` / `user.email`) in the git config
  of the user running terraform — configure one in CI jobs.

FEATURES:

* New provider attribute `branch` (with `repo_url`): which branch of the
  password store to read and write; defaults to the remote HEAD branch.
* Every create/update/delete now syncs safely with the store remote:
  fetch → rebase onto `origin/<branch>` → commit → push, retrying rejected
  pushes up to 3 times. Concurrent same-secret writes fail loudly instead of
  silently picking a winner. A lock file protects the checkout across
  terraform processes.
* Commits are labelled `terraform-provider-pass: write <path>` /
  `terraform-provider-pass: delete <path>`; author and signing follow the
  operator's git config.
* Reading a secret that was deleted from the store outside terraform now
  clears it from state (terraform plans a recreation) instead of failing, and
  destroying an already-deleted secret succeeds.
* Creating a `pass_password` resource now fails instead of silently
  overwriting a secret that already exists at `path` but isn't tracked in
  terraform state (e.g. written by another team or the `pass`/`gopass` CLI).
  The error names the `terraform import` command to adopt it instead. See
  `docs/adr/0002-create-refuses-to-overwrite-untracked-secrets.md`.

ENHANCEMENTS:

* Upgrade gopass 1.9.2 → 1.16.1 (via its public API only) and Terraform
  plugin SDK to 2.40.1.
* Refresh dependencies to remediate known vulnerabilities: Go toolchain
  1.26.4, `terraform-plugin-docs` 0.25.0, `golang.org/x/net` 0.56.0,
  `golang.org/x/crypto` 0.53.0, plus transitive bumps from `go mod tidy`.
  `govulncheck` now reports 0 reachable vulnerabilities in provider code.
* The on-disk secret format is byte-identical to what previous versions
  wrote; stores are fully shared with humans using the `pass` CLI.

## 1.7.7 (April 7, 2026)

ENHANCEMENTS:

* Build with the latest Go release
* Update Terraform plugin SDK and gRPC dependencies

## 1.7.6 (March 7, 2026)

ENHANCEMENTS:

* Dependency updates (terraform-plugin-sdk, circl)

## 1.7.5 (December 9, 2025)

ENHANCEMENTS:

* Dependency updates, including golang.org/x/crypto security fixes

## 1.7.4 (June 13, 2025)

ENHANCEMENTS:

* Dependency updates fixing CVEs (golang.org/x/net, circl)

## 1.7.3 (March 26, 2025)

ENHANCEMENTS:

* Update Terraform plugin SDK and terraform-plugin-docs
* Add release dry-run workflow

## 1.7.2 (January 22, 2025)

ENHANCEMENTS:

* Update goreleaser to 2.5.x and bump vulnerable dependencies
* Document local, manual testing

## 1.7.1 (November 1, 2023)

BUG FIXES:

* Bump gRPC and golang.org/x/net to fix vulnerabilities

## 1.7.0 (February 24, 2023)

BUG FIXES:

* Bump golang.org/x/net and golang.org/x/text to fix CVEs

## 1.6.0 (January 17, 2023)

ENHANCEMENTS:

* Enable GPG signing of releases
* Dependency updates

## 1.5.1 (November 11, 2022)

ENHANCEMENTS:

* Dependency updates

## 1.5.0 (April 26, 2022)

ENHANCEMENTS:
* Build with Go 1.18
* Use Terraform SDK v2
* Support ARM-based Macs

## 1.4.0 (Aug 19, 2020)

ENHANCEMENTS:

* Add mutex to protect against concurrent operations (GH #36)
* Build on Go 1.13

BUG FIXES:

* Update gopass dependencies


## 1.3.0 (July 24, 2020)

ENHANCEMENTS:

* Set password resources values as sensitive
* Return ResourceRead errors
* Fix build with Go 1.13
* Use new Terraform plugin SDK

## 1.2.1 (June 05, 2019)

ENHANCEMENTS:

* Use Terraform v0.12.0

## 1.2.0 (May 21, 2019)

IMPROVEMENTS:

* Update Gopass dependencies
* Use Terraform v0.12-beta's API
* Improve build config, add CI

## 1.1.1 (Sep 21, 2018)

IMPROVEMENTS:

* Support for single-line password secret

BUG FIXES:

* provider: return an error if the store is not initialized

## 1.1.0 (Jun 25, 2018)

IMPROVEMENTS:

* Support newer versions of gopass

FEATURES:

* Expose entire secret contents in `.full` data source attribute

## 1.0.1 (Mar 5, 2018)

BUG FIXES:

* datasource/passwordReturn errors when setter fails

## 1.0.0 (Oct 4, 2017)

IMPROVEMENTS:

* Port to gopass

## 0.1.4 (Jul 4, 2017)

FEATURES:

* provider: refresh password store by default

## 0.1.3 (Jul 4, 2017)

FEATURES:

* resource/password: new resource
* add tests

## 0.1.2 (Feb 15, 2017)

BUG FIXES:

* datasource/password: fix attribute name

## 0.1.1 (Feb 9, 2017)

BUG FIXES:

* datasource/password: don't fail if unmarshaling fails

## 0.1.0 (Jan 16, 2017)

* Initial release
