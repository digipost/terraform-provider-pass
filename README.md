Pass Terraform Provider
=======================

> This provider was forked from the now-defunct [camptocamp/terraform-provider-pass](https://github.com/camptocamp/terraform-provider-pass) and took some inspirational patches from [another fork](https://github.com/mecodia/terraform-provider-pass) which is based on the 2.x releases.

This provider adds integration between Terraform and [Pass][] and [Gopass][] password stores.

[Pass][] is a password store using gpg to encrypt password and git to version.
[Gopass][] is a rewrite of the pass password manager in Go with the aim of making it cross-platform and adding additional features.

Requirements
------------

-	[Terraform](https://www.terraform.io/downloads.html) 0.12.x
-	[Go](https://golang.org/doc/install) 1.18

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

Installing the provider
-----------------------

After building the provider, install it using the Terraform instructions for [installing a third party provider](https://www.terraform.io/docs/configuration/providers.html#third-party-plugins) or [in-house providers](https://www.terraform.io/language/providers/requirements#in-house-providers).

Example
----------------------

```hcl
provider "pass" {
  store_dir = "/srv/password-store"    # defaults to $PASSWORD_STORE_DIR
  refresh_store = false                # do not call `git pull`
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
The provider takes the following arguments:
- `store_dir` - (Optional) Path to your password store, defaults to `$PASSWORD_STORE_DIR`
- `refresh_store` - (Optional) Boolean whether to call `git pull` when configuring the provider, defaults to `true`


### The `pass_password` resource
#### Argument Reference
The resource takes the following arguments:
- `path` - Full path from which a password will be read
- `password` - Secret password
- `data` - (Optional) Additional secret data

#### Attribute Reference
The following attributes are exported:

- `path` - Full path from which the password was read
- `password` - Secret password
- `data` - Additional secret data
- `body` - Raw secret data if not YAML
- `full` - Entire secret contents


### The `pass_password` data source
#### Argument Reference
The data source takes the following arguments:
 - `path` - Full path from which a password will be read

#### Attribute Reference
The following attributes are exported:

- `path` - Full path from which the password was read
- `password` - Secret password
- `data` - Additional secret data
- `body` - Raw secret data if not YAML
- `full` - Entire secret contents


[Pass]: https://www.passwordstore.org/
[Gopass]: https://www.justwatch.com/gopass/
