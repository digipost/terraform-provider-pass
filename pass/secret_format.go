package pass

import (
	"fmt"
	"sort"
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

// checkSecretRepresentable reports whether raw can be held losslessly by the
// pass_password resource: a password line plus an optional `---`-delimited
// YAML map of string or integer values. A nil return still permits
// representation-only rewrites on the next write (key reordering, quoting);
// a non-nil error describes the content that would be lost or corrupted.
// The error names no path or remedy — callers add those.
func checkSecretRepresentable(raw []byte) error {
	full := string(raw)
	if strings.Contains(full, "\r") {
		return errors.New("the secret has carriage-return (CRLF) line endings, which would corrupt the password and be rewritten LF-only")
	}

	password, body, _ := strings.Cut(full, "\n")
	if password == "---" {
		return errors.New("the first line is the YAML document marker ---, so the secret has no password line")
	}
	if strings.TrimSpace(body) == "" {
		// A password-only secret; trailing blank lines are normalized away
		// on the next write, but no content is lost.
		return nil
	}
	if !strings.HasPrefix(body, "---\n") {
		return errors.New("the secret has free-form text after the password line, which the data attribute cannot hold")
	}
	for _, line := range strings.Split(body[len("---\n"):], "\n") {
		// Document markers are only recognized unindented, so block-scalar
		// content cannot false-positive here.
		if line == "---" || line == "..." || strings.HasPrefix(line, "--- ") {
			return errors.New("the YAML section contains more than one document; only the first would survive")
		}
	}

	// Unmarshal with interface{} keys: unlike parseSecretBytes' string-keyed
	// map, this errors on complex mapping keys instead of dropping them.
	parsed := make(map[interface{}]interface{})
	if err := yaml.Unmarshal([]byte(body), &parsed); err != nil {
		return errors.New("the text after --- is not a YAML map, which is the only body the data attribute can hold")
	}

	var lossy []string
	for k, v := range parsed {
		key, ok := k.(string)
		if !ok {
			lossy = append(lossy, fmt.Sprintf("%v", k))
			continue
		}
		switch v.(type) {
		case string, int, int64, uint64:
		default:
			// float64, bool, nil, nested maps and lists all get flattened
			// through fmt.Sprintf("%v"): 1.10 becomes "1.1", yes becomes
			// "true", an empty value becomes "<nil>", nesting becomes Go
			// debug syntax.
			lossy = append(lossy, key)
		}
	}
	if len(lossy) > 0 {
		sort.Strings(lossy)

		return fmt.Errorf("data values must be plain strings or integers, but these keys hold floats, booleans, empty values or nested structures that would be corrupted on rewrite (e.g. 1.10 becomes \"1.1\"): %s", strings.Join(lossy, ", "))
	}

	return nil
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
