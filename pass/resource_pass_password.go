package pass

import (
	"context"
	"fmt"

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
	if err := d.Set("password", parsed.password); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("data", parsed.data); err != nil {
		return diag.FromErr(err)
	}

	return nil
}
