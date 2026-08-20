# terraform-provider-pass

Terraform provider that reads and writes secrets in a shared, git-backed
[pass](https://www.passwordstore.org/) password store.

## Language

**Password store**:
The git repository of GPG-encrypted secrets in pass format (e.g. `digipost-passwords`).
Owned by humans using the `pass` CLI daily; the provider is a guest and must stay
byte-compatible with what `pass` reads and writes.
_Avoid_: vault, secret store, module

**Secret**:
A password plus optional key-value data, stored as one GPG-encrypted file in the
password store. The password is the first line; data follows as a YAML document.
_Avoid_: credential, entry

**Path**:
The slash-separated location of a secret within the password store
(e.g. `Utvikling/Azure/acr_ai_read_token`). Doubles as the Terraform resource id.

**Store cache**:
The provider-managed local clone of the password store, reused across Terraform runs.
Disposable: it can be deleted and re-cloned at any time without data loss.
_Avoid_: store_dir, module checkout, working copy
