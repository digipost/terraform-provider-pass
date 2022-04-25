package pass

import (
	"fmt"
	"testing"

	r "github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestResourcePassword(t *testing.T) {
	r.Test(t, r.TestCase{
		ProviderFactories: testProviderFactory,
		Steps: []r.TestStep{
			{
				Config: testResourcePasswordInitialConfig,
				Check:  testResourcePasswordInitialCheck,
			},
			{
				Config: testResourcePasswordUpdateConfig,
				Check:  testResourcePasswordUpdateCheck,
			},
		},
	})
}

var testResourcePasswordInitialConfig = `

resource "pass_password" "test" {
    path = "secret/foo"
	password = "0123456789"
    data = {
        zip = "zap"
	}
}

`

func testResourcePasswordInitialCheck(s *terraform.State) error {
	resourceState := s.Modules[0].Resources["pass_password.test"]
	if resourceState == nil {
		return fmt.Errorf("resource not found in state")
	}

	instanceState := resourceState.Primary
	if instanceState == nil {
		return fmt.Errorf("resource has no primary instance")
	}

	path := instanceState.ID

	if path != instanceState.Attributes["path"] {
		return fmt.Errorf("id doesn't match path")
	}
	if path != "secret/foo" {
		return fmt.Errorf("unexpected secret path")
	}

	if got, want := instanceState.Attributes["password"], "0123456789"; got != want {
		return fmt.Errorf("data contains %s; want %s", got, want)
	}

	if got, want := instanceState.Attributes["data.zip"], "zap"; got != want {
		return fmt.Errorf("data contains %s; want %s", got, want)
	}

	return nil
}

var testResourcePasswordUpdateConfig = `

resource "pass_password" "test" {
    path = "secret/foo"
	password = "012345678"
    data = {
        zip = "zoop"
	}
}

`

func testResourcePasswordUpdateCheck(s *terraform.State) error {
	resourceState := s.Modules[0].Resources["pass_password.test"]
	if resourceState == nil {
		return fmt.Errorf("resource not found in state")
	}

	instanceState := resourceState.Primary
	if instanceState == nil {
		return fmt.Errorf("resource has no primary instance")
	}

	if got, want := instanceState.Attributes["data.zip"], "zoop"; got != want {
		return fmt.Errorf("data on test instance contains %s; want %s", got, want)
	}

	return nil
}
