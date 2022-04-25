package pass

import (
	"context"
	"fmt"
	"log"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"gopkg.in/yaml.v2"

	"github.com/gopasspw/gopass/pkg/store/secret"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/pkg/errors"
)

func passPasswordResource() *schema.Resource {
	return &schema.Resource{
		CreateContext: passPasswordResourceWrite,
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

func passPasswordResourceWrite(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	path := d.Get("path").(string)

	pp := meta.(*passProvider)
	pp.mutex.Lock()
	defer pp.mutex.Unlock()
	st := pp.store

	passwd := d.Get("password").(string)

	data := d.Get("data").(map[string]interface{})
	dataYaml, err := yaml.Marshal(&data)
	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "failed to marshal data as YAML for %s", path))
	}

	if len(data) == 0 {
		sec := secret.New(passwd, fmt.Sprintf(""))
		err = st.Set(ctx, path, sec)
	} else {
		sec := secret.New(passwd, fmt.Sprintf("---\n%s", dataYaml))
		err = st.Set(ctx, path, sec)
	}

	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "failed to write secret at %s", path))
	}

	d.SetId(path)

	return nil
}

func passPasswordResourceDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	path := d.Id()

	pp := meta.(*passProvider)
	pp.mutex.Lock()
	defer pp.mutex.Unlock()
	st := pp.store
	log.Printf("[DEBUG] Deleting generic Vault from %s", path)
	err := st.Delete(ctx, path)
	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "failed to delete password at %s", path))
	}

	return nil
}

func passPasswordResourceRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	path := d.Id()

	pp := meta.(*passProvider)
	pp.mutex.Lock()
	defer pp.mutex.Unlock()
	st := pp.store
	sec, err := st.Get(ctx, path)
	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "failed to retrieve password at %s", path))
	}

	d.Set("password", sec.Password())
	d.Set("data", sec.Data())

	return nil
}
