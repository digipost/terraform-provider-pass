package main

/* Bootstrap the plugin for Pass */

import (
	"github.com/digipost/terraform-provider-pass/pass"
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderFunc: pass.Provider,
	})
}
