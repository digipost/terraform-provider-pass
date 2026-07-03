package pass

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/pkg/errors"
)

func passwordDataSource() *schema.Resource {
	return &schema.Resource{
		ReadContext: passwordDataSourceRead,
		Schema: map[string]*schema.Schema{
			"path": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Full path from which a password will be read.",
			},

			"password": {
				Type:        schema.TypeString,
				Computed:    true,
				Sensitive:   true,
				Description: "secret password.",
			},

			"data": {
				Type:        schema.TypeMap,
				Computed:    true,
				Sensitive:   true,
				Description: "additional secret data.",
			},

			"body": {
				Type:        schema.TypeString,
				Computed:    true,
				Sensitive:   true,
				Description: "raw secret data if not YAML.",
			},

			"full": {
				Type:        schema.TypeString,
				Computed:    true,
				Sensitive:   true,
				Description: "entire secret contents",
			},
		},
	}
}

func passwordDataSourceRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	path := d.Get("path").(string)
	pp := meta.(*passProvider)

	tflog.Debug(ctx, fmt.Sprintf("Reading %s from Pass", path))

	sec, err := pp.getSecret(ctx, path)
	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "failed to read password at %s", path))
	}

	d.SetId(path)

	parsed := parseSecretBytes(sec.Bytes())
	if err := d.Set("password", parsed.password); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("data", parsed.data); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("body", parsed.body); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("full", parsed.full); err != nil {
		return diag.FromErr(err)
	}

	return nil
}
