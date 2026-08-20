package pass

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/pkg/errors"
)

func passPasswordResource() *schema.Resource {
	return &schema.Resource{
		CreateContext: passPasswordResourceCreate,
		UpdateContext: passPasswordResourceWrite,
		DeleteContext: passPasswordResourceDelete,
		ReadContext:   passPasswordResourceRead,
		Importer: &schema.ResourceImporter{
			StateContext: passPasswordResourceImport,
		},

		Schema: map[string]*schema.Schema{
			"path": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "Full path where the pass data will be written",
			},

			"password": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Secret password",
				Sensitive:   true,
			},

			"data": {
				Type:        schema.TypeMap,
				Optional:    true,
				Description: "Additional key-value data",
				Sensitive:   true,
			},
		},
	}
}

// passPasswordResourceImport guards `terraform import` against the two ways
// a passthrough import silently destroys shared-store content (see
// docs/adr/0003-import-refuses-unrepresentable-secrets.md): a non-canonical
// import ID (gopass normalizes it on read, so the mismatched ForceNew path
// forces a destructive replace on the next apply), and a secret whose
// content the resource schema cannot carry (the next write would truncate
// or corrupt it).
func passPasswordResourceImport(ctx context.Context, d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	path := d.Id()
	pp := meta.(*passProvider)

	if canonical := canonicalStorePath(path); canonical != path {
		if canonical == "" {
			return nil, fmt.Errorf("cannot import %q: the import ID must be the secret's store path", path)
		}

		return nil, fmt.Errorf("cannot import %q: the import ID must be the extension-less store path relative to the store root, as `pass ls` shows it — did you mean %q?", path, canonical)
	}

	sec, err := pp.getSecret(ctx, path)
	if isNotFoundErr(err) {
		return nil, fmt.Errorf("cannot import %s: no secret exists at that path in the password store; paths are extension-less and relative (e.g. Utvikling/Azure/db_password, not a .gpg filename) — check `pass ls`", path)
	}
	if err != nil {
		return nil, errors.Wrapf(err, "failed to retrieve password at %s", path)
	}

	if err := checkSecretRepresentable(sec.Bytes()); err != nil {
		return nil, fmt.Errorf("cannot import %s: %v; the next terraform apply would silently rewrite the secret in the shared password store — keep managing it with the pass CLI, or rewrite it into the provider's format (password line, then optional ----delimited YAML string data) first", path, err)
	}

	return []*schema.ResourceData{d}, nil
}

// canonicalStorePath strips the decorations gopass silently normalizes away
// on read (surrounding whitespace, leading/trailing/doubled slashes, a
// .gpg/.txt filename suffix). The importer refuses any ID that differs from
// its canonical form instead of auto-fixing it, so the state path always
// matches what the user typed and what the config must say.
func canonicalStorePath(path string) string {
	p := path
	for {
		q := strings.TrimSpace(p)
		q = strings.Trim(q, "/")
		q = strings.TrimSuffix(q, ".gpg")
		q = strings.TrimSuffix(q, ".txt")
		if q == p {
			break
		}
		p = q
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}

	return p
}

// passPasswordResourceCreate refuses to create a resource on top of a
// secret that already exists in the store but isn't tracked in terraform
// state (e.g. added by another team, another tool, or a previous
// out-of-band `pass`/`gopass` invocation). Without this check, Create and
// Update share the same unconditional overwrite behavior and a plan the
// operator believes is additive would silently destroy that secret.
func passPasswordResourceCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	path := d.Get("path").(string)
	pp := meta.(*passProvider)

	exists, err := pp.secretExists(ctx, path)
	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "failed to check for an existing secret at %s", path))
	}
	if exists {
		return diag.Errorf("refusing to create %s: a secret already exists there in the password store and is not managed by this resource; import it first with `terraform import <resource address> %s` or choose a different path", path, path)
	}

	return passPasswordResourceWrite(ctx, d, meta)
}

// secretExists reports whether path already holds a secret in the store,
// without decrypting or exposing its contents.
func (pp *passProvider) secretExists(ctx context.Context, path string) (bool, error) {
	_, err := pp.getSecret(ctx, path)
	if isNotFoundErr(err) {
		return false, nil
	}

	return err == nil, err
}

func passPasswordResourceWrite(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	path := d.Get("path").(string)
	pp := meta.(*passProvider)

	secretBytes, err := buildSecretBytes(d.Get("password").(string), d.Get("data").(map[string]interface{}))
	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "failed to construct secret for %s", path))
	}

	err = pp.store.withWrite(ctx, "terraform-provider-pass: write "+path, func(ctx context.Context) error {
		return pp.gp.Set(gopassWriteCtx(ctx), path, rawSecret(secretBytes))
	})
	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "failed to write secret at %s", path))
	}

	d.SetId(path)

	return nil
}

func passPasswordResourceDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	path := d.Id()
	pp := meta.(*passProvider)

	tflog.Debug(ctx, fmt.Sprintf("Deleting from store: %s", path))
	err := pp.store.withWrite(ctx, "terraform-provider-pass: delete "+path, func(ctx context.Context) error {
		err := pp.gp.Remove(gopassWriteCtx(ctx), path)
		if isNotFoundErr(err) {
			// Someone already removed it from the store: deletion is
			// idempotent.
			return nil
		}

		return err
	})
	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "failed to delete password at %s", path))
	}

	return nil
}

func passPasswordResourceRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	path := d.Id()
	pp := meta.(*passProvider)

	sec, err := pp.getSecret(ctx, path)
	if isNotFoundErr(err) {
		// The secret was removed from the store outside terraform; let
		// terraform plan its recreation instead of failing.
		d.SetId("")

		return nil
	}
	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "failed to retrieve password at %s", path))
	}

	parsed := parseSecretBytes(sec.Bytes())
	if err := d.Set("path", path); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("password", parsed.password); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("data", parsed.data); err != nil {
		return diag.FromErr(err)
	}

	return nil
}
