package pass

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/blang/semver"
	"github.com/gopasspw/gopass/pkg/action"
	_ "github.com/gopasspw/gopass/pkg/backend/storage"
	"github.com/gopasspw/gopass/pkg/config"
	"github.com/gopasspw/gopass/pkg/store/root"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/pkg/errors"
)

type passProvider struct {
	store *root.Store
	mutex *sync.Mutex
}

func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"store_dir": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("PASSWORD_STORE_DIR", ""),
				Description: "Password storage directory to use.",
			},
			"refresh_store": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether or not call `pass git pull`.",
			},
		},

		ConfigureContextFunc: providerConfigure,

		DataSourcesMap: map[string]*schema.Resource{
			"pass_password": passwordDataSource(),
		},

		ResourcesMap: map[string]*schema.Resource{
			"pass_password": passPasswordResource(),
		},
	}
}

func providerConfigure(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
	os.Setenv("PASSWORD_STORE_DIR", d.Get("store_dir").(string))

	act, err := action.New(ctx, config.Load(), semver.Version{})
	if err != nil {
		return nil, diag.FromErr(errors.Wrap(err, "error instantiating password store"))
	}

	if ok, err := act.Store.Initialized(ctx); !ok || err != nil {
		return nil, diag.FromErr(errors.New(fmt.Sprintf("password-store not initialized: %s", err)))
	}
	st := act.Store

	if d.Get("refresh_store").(bool) {
		log.Printf("[DEBUG] Pull pass repository")
		err := st.GitPull(ctx, "", "origin", "master")

		if err != nil {
			return nil, diag.FromErr(errors.Wrap(err, "error refreshing password store"))
		}
	}

	pp := &passProvider{
		store: st,
		mutex: &sync.Mutex{},
	}

	return pp, nil
}
