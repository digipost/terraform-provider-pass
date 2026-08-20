Pass Terraform Provider
=======================

> This provider was forked from the now-defunct [camptocamp/terraform-provider-pass](https://github.com/camptocamp/terraform-provider-pass) and took some inspirational patches from [another fork](https://github.com/mecodia/terraform-provider-pass) which is based on the 2.x releases.

This provider adds integration between Terraform and [Pass][] password stores
(also compatible with stores managed by [Gopass][]).

[Pass][] is a password store using gpg to encrypt passwords and git to version them.
The provider treats the store as shared with humans running the `pass` CLI: it
writes the exact same on-disk format, and every change becomes a git commit
(`terraform-provider-pass: write <path>`) pushed to the store's remote.

Since 2.0 the provider manages its own clone of the password store: point it at
the store's git URL with `repo_url` and it clones into a per-repository cache
(`~/.cache/terraform-provider-pass/`), refreshes it when the provider configures,
and wraps every write in fetch → rebase → commit → push with retries. See
`docs/index.md` for details and for migrating from the 1.x
`store_dir = ".terraform/modules/..."` workaround, and
`docs/adr/0001-provider-owns-git-gopass-only-encrypts.md` for the architecture.

Requirements
------------

-	[Terraform](https://www.terraform.io/downloads.html) 0.12.x or newer
-	[Go](https://golang.org/doc/install) 1.26 (to build the provider)
- [goreleaser](https://goreleaser.com/) >= 2.5.1 (to release the provider)
- `git` and `gpg` binaries on `PATH` at runtime, with a GPG key that can
  decrypt the store and a git identity (`user.name` / `user.email`) configured
  — CI jobs included

Building The Provider
---------------------

Download the provider source code

```sh
$ git clone https://github.com/digipost/terraform-provider-pass.git
```

Enter the provider directory and build the provider

```sh
$ cd terraform-provider-pass
$ make
```

Testing the binary locally
--------------------------

1. Set up a developer override in Terraform for the provider, as described in https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers
This amounts to creating or updating a `~/.terraformrc` file with contents of:
```
provider_installation {

  dev_overrides {
      "digipost/pass" = "${GOPATH}/bin"
  }

  # For all other providers, install them directly from their origin provider
  # registries as normal. If you omit this, Terraform will _only_ use
  # the dev_overrides block, and so no other providers will be available.
  direct {}
}
```
You must substitute `${GOPATH}` with the actual value of your shell environment variable, `GOPATH`, as 
environment variable substitution in that file, does not work.

2. Install the binary of the provider locally: `go install .`
   This places a copy of the binary in the folder configured above. 

3. Create a new folder to hold a new minimal Terraform configuration, a `main.tf` file with contents like:
```
terraform {
  required_providers {
    pass = {
      source  = "digipost/pass"
    }
  }
}
provider "pass" {
  store_dir     = "./test"   # a local store: a directory containing a .gpg-id file
  refresh_store = false
}

resource "pass_password" "test" {
  path = "foo/bar/username"
  password = "mysecretpassword"
}

data "pass_password" "test" {
  path = "foo/bar/username"
  depends_on = [ pass_password.test ]
}

output "testdata" {
  value     = data.pass_password.test
  sensitive = true
}

```

4. Change into the directory and execute `terraform plan`, `terraform apply` etc.
This should produce no errors.

The unit tests cover the git layer against local bare repositories and the full
provider stack (including a real-GPG round trip when `gpg` is installed):

```sh
$ go test ./...
```

Installing the provider
-----------------------

After building the provider, install it using the Terraform instructions for [installing a third party provider](https://www.terraform.io/docs/configuration/providers.html#third-party-plugins) or [in-house providers](https://www.terraform.io/language/providers/requirements#in-house-providers).

Example
----------------------

```hcl
provider "pass" {
  repo_url = "git@github.com:example-org/passwords.git"  # provider manages its own clone
  # branch = "main"                                      # defaults to the remote HEAD branch
}


resource "pass_password" "test" {
  path = "secret/foo"
  password = "0123456789"
  data = {
    zip = "zap"
  }
}

data "pass_password" "test" {
  path = "${pass_password.test.path}"
}
```

Usage
----------------------

### The `pass` provider
#### Argument Reference
The provider takes the following arguments (exactly one of `repo_url` and `store_dir` must be set):
- `repo_url` - (Optional) Git URL of the password store repository. The provider maintains its own clone in `~/.cache/terraform-provider-pass/` and pushes every write
- `branch` - (Optional, only with `repo_url`) Branch of the store to read and write, defaults to the remote HEAD branch
- `store_dir` - (Optional) Path to an existing local password store, defaults to `$PASSWORD_STORE_DIR`. Must be a git clone checked out on a branch, or (with `refresh_store = false`) a plain store directory
- `refresh_store` - (Optional) Boolean whether to update the store from its git remote when the provider configures, defaults to `true`. With `false` reads may be stale, but writes still push


### The `pass_password` resource
#### Argument Reference
The resource takes the following arguments:
- `path` - Full path where the secret is written
- `password` - Secret password
- `data` - (Optional) Additional key-value data

#### Attribute Reference
The following attributes are exported:

- `path` - Full path from which the password was read
- `password` - Secret password
- `data` - Additional secret data


### The `pass_password` data source
#### Argument Reference
The data source takes the following arguments:
 - `path` - Full path from which a password will be read

#### Attribute Reference
The following attributes are exported (all sensitive):

- `path` - Full path from which the password was read
- `password` - Secret password
- `data` - Additional secret data
- `body` - Raw secret data if not YAML
- `full` - Entire secret contents


[Pass]: https://www.passwordstore.org/
[Gopass]: https://www.gopass.pw/
