package pass

import (
	"context"
	"fmt"
	"log"

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
				Description: "secret password.",
			},

			"data": {
				Type:        schema.TypeMap,
				Computed:    true,
				Description: "additional secret data.",
			},

			"body": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "raw secret data if not YAML.",
			},

			"full": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "entire secret contents",
			},
		},
	}
}

func passwordDataSourceRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	path := d.Get("path").(string)

	pp := meta.(*passProvider)
	pp.mutex.Lock()
	defer pp.mutex.Unlock()
	st := pp.store
	tflog.Debug(ctx, fmt.Sprintf("Reading %s from Pass", path))

	sec, err := st.Get(ctx, path)
	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "failed to read password at %s", path))
	}

	d.SetId(path)

	if err := d.Set("password", sec.Password()); err != nil {
		log.Printf("[ERROR] Error when setting password: %v", err)
		return diag.FromErr(err)
	}
	if err := d.Set("data", sec.Data()); err != nil {
		log.Printf("[ERROR] Error when setting data: %v", err)
		return diag.FromErr(err)
	}
	if err := d.Set("body", sec.Body()); err != nil {
		log.Printf("[ERROR] Error when setting body: %v", err)
		return diag.FromErr(err)
	}
	if err := d.Set("full", sec.String()); err != nil {
		log.Printf("[ERROR] Error when setting full: %v", err)
		return diag.FromErr(err)
	}

	return nil
}
