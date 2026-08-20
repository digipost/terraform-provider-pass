package pass

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

// The password store is shared with humans using the pass CLI, so the
// on-disk plaintext layout is a compatibility contract. It must stay
// byte-identical to what gopass v1.9.2's secret.New produced:
//
//	<password>                          (no trailing newline, no data)
//	<password>\n---\n<yaml map>         (with data; yaml.v2, sorted keys)

// rawSecret is an opaque gopass.Byter: gopass encrypts exactly these bytes.
type rawSecret []byte

func (r rawSecret) Bytes() []byte { return r }

// buildSecretBytes constructs the plaintext for a secret from the resource
// attributes.
func buildSecretBytes(password string, data map[string]interface{}) ([]byte, error) {
	if len(data) == 0 {
		return []byte(password), nil
	}

	dataYaml, err := yaml.Marshal(&data)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal data as YAML")
	}

	return []byte(password + "\n---\n" + string(dataYaml)), nil
}

// storedSecret is the provider's view of a decrypted secret.
type storedSecret struct {
	password string
	body     string
	data     map[string]string
	full     string
}

// parseSecretBytes splits a secret's plaintext the same way gopass v1.9.2
// did: first line is the password, the rest is the body, and a body forming
// a YAML document (`---\n...`) additionally yields key-value data. A body
// that fails to parse as YAML is not an error; data is just empty.
func parseSecretBytes(raw []byte) storedSecret {
	sec := storedSecret{full: string(raw)}

	sec.password, sec.body, _ = strings.Cut(sec.full, "\n")

	if strings.HasPrefix(sec.body, "---\n") || sec.password == "---" {
		parsed := make(map[string]interface{})
		if err := yaml.Unmarshal([]byte(sec.body), &parsed); err == nil {
			sec.data = make(map[string]string, len(parsed))
			for k, v := range parsed {
				sec.data[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	return sec
}
