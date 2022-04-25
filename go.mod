module github.com/camptocamp/terraform-provider-pass

replace github.com/stretchr/testify => github.com/stretchrcom/testify v1.6.1

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/gopasspw/gopass v1.9.2
	github.com/hashicorp/terraform-plugin-sdk/v2 v2.14.0
	github.com/mattn/go-colorable v0.1.7 // indirect
	github.com/pkg/errors v0.9.1
	gopkg.in/yaml.v2 v2.3.0
	gopkg.in/yaml.v3 v3.0.0-20200615113413-eeeca48fe776 // indirect
)

go 1.13
