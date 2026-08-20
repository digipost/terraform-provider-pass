package pass

import (
	"reflect"
	"strings"
	"testing"
)

// The expected byte strings in these tests are the exact output of the
// v1.9.2 write path (gopass secret.New + yaml.v2). They are a compatibility
// contract with existing password stores; do not "fix" them.
func TestBuildSecretBytes(t *testing.T) {
	tests := []struct {
		name     string
		password string
		data     map[string]interface{}
		want     string
	}{
		{
			name:     "password only has no trailing newline",
			password: "0123456789",
			data:     nil,
			want:     "0123456789",
		},
		{
			name:     "empty data map treated as no data",
			password: "hunter2",
			data:     map[string]interface{}{},
			want:     "hunter2",
		},
		{
			name:     "single data key",
			password: "0123456789",
			data:     map[string]interface{}{"zip": "zap"},
			want:     "0123456789\n---\nzip: zap\n",
		},
		{
			name:     "data keys are sorted alphabetically",
			password: "pw",
			data:     map[string]interface{}{"user": "alice", "host": "example.com"},
			want:     "pw\n---\nhost: example.com\nuser: alice\n",
		},
		{
			name:     "empty password with data",
			password: "",
			data:     map[string]interface{}{"k": "v"},
			want:     "\n---\nk: v\n",
		},
		{
			name:     "value requiring yaml quoting",
			password: "pw",
			data:     map[string]interface{}{"weird": "a: b"},
			want:     "pw\n---\nweird: 'a: b'\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildSecretBytes(tt.password, tt.data)
			if err != nil {
				t.Fatalf("buildSecretBytes: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("buildSecretBytes = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestParseSecretBytes(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantPassword string
		wantBody     string
		wantData     map[string]string
	}{
		{
			name:         "password only",
			raw:          "0123456789",
			wantPassword: "0123456789",
			wantBody:     "",
			wantData:     nil,
		},
		{
			name:         "password with yaml data",
			raw:          "0123456789\n---\nzip: zap\n",
			wantPassword: "0123456789",
			wantBody:     "---\nzip: zap\n",
			wantData:     map[string]string{"zip": "zap"},
		},
		{
			name:         "multiple data keys",
			raw:          "pw\n---\nhost: example.com\nuser: alice\n",
			wantPassword: "pw",
			wantBody:     "---\nhost: example.com\nuser: alice\n",
			wantData:     map[string]string{"host": "example.com", "user": "alice"},
		},
		{
			name:         "non-yaml body is preserved raw",
			raw:          "pw\nsome free-form\nnotes here",
			wantPassword: "pw",
			wantBody:     "some free-form\nnotes here",
			wantData:     nil,
		},
		{
			name:         "invalid yaml after separator yields no data",
			raw:          "pw\n---\n\t: not yaml",
			wantPassword: "pw",
			wantBody:     "---\n\t: not yaml",
			wantData:     nil,
		},
		{
			name:         "numeric values become strings",
			raw:          "pw\n---\nport: 42\n",
			wantPassword: "pw",
			wantBody:     "---\nport: 42\n",
			wantData:     map[string]string{"port": "42"},
		},
		{
			name:         "empty input",
			raw:          "",
			wantPassword: "",
			wantBody:     "",
			wantData:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSecretBytes([]byte(tt.raw))
			if got.password != tt.wantPassword {
				t.Errorf("password = %q; want %q", got.password, tt.wantPassword)
			}
			if got.body != tt.wantBody {
				t.Errorf("body = %q; want %q", got.body, tt.wantBody)
			}
			if got.full != tt.raw {
				t.Errorf("full = %q; want %q", got.full, tt.raw)
			}
			if !reflect.DeepEqual(got.data, tt.wantData) {
				t.Errorf("data = %#v; want %#v", got.data, tt.wantData)
			}
		})
	}
}

// TestCheckSecretRepresentable pins which pre-existing secrets import may
// adopt: anything a later write would truncate or corrupt must be refused,
// while representation-only normalization (key order, quoting) is allowed.
func TestCheckSecretRepresentable(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		// wantErr is a substring of the expected error; empty means allowed.
		wantErr string
	}{
		{name: "canonical secret with data", raw: "pw\n---\nzip: zap\n"},
		{name: "password only", raw: "hunter2"},
		{name: "password with trailing newline", raw: "hunter2\n"},
		{name: "empty file", raw: ""},
		{name: "whitespace-only body", raw: "pw\n\n \n"},
		{name: "bare yaml marker body", raw: "pw\n---\n"},
		{name: "integer value", raw: "pw\n---\nport: 42\n"},
		{name: "unsorted keys are formatting only", raw: "pw\n---\nuser: alice\nhost: example.com\n"},
		{
			name:    "free-form body",
			raw:     "hunter2\nRotated 2026-01-05 by ops\n",
			wantErr: "free-form text",
		},
		{
			name:    "invalid yaml after separator",
			raw:     "pw\n---\n\t: not yaml",
			wantErr: "not a YAML map",
		},
		{
			name:    "yaml list body",
			raw:     "pw\n---\n- a\n- b\n",
			wantErr: "not a YAML map",
		},
		{
			name:    "float value",
			raw:     "pw\n---\nratio: 1.10\n",
			wantErr: "ratio",
		},
		{
			name:    "boolean value",
			raw:     "pw\n---\nflag: yes\n",
			wantErr: "flag",
		},
		{
			name:    "null value",
			raw:     "pw\n---\nempty:\n",
			wantErr: "empty",
		},
		{
			name:    "nested map value",
			raw:     "pw\n---\nnested:\n  user: x\n",
			wantErr: "nested",
		},
		{
			name:    "list value",
			raw:     "pw\n---\nitems:\n  - a\n",
			wantErr: "items",
		},
		{
			name:    "lossy keys listed sorted",
			raw:     "pw\n---\nratio: 1.10\nflag: yes\nnested:\n  user: x\n",
			wantErr: "flag, nested, ratio",
		},
		{
			name:    "non-string key",
			raw:     "pw\n---\n1: x\n",
			wantErr: "1",
		},
		{
			name:    "yaml document as first line",
			raw:     "---\nzip: zap\n",
			wantErr: "no password line",
		},
		{
			name:    "second yaml document",
			raw:     "pw\n---\nzip: zap\n---\nother: doc\n",
			wantErr: "more than one document",
		},
		{
			name:    "crlf line endings",
			raw:     "pw\r\n---\r\nzip: zap\r\n",
			wantErr: "CRLF",
		},
		{
			name:    "single line with trailing carriage return",
			raw:     "pw\r\n",
			wantErr: "CRLF",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSecretRepresentable([]byte(tt.raw))
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("checkSecretRepresentable(%q) = %v; want nil", tt.raw, err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("checkSecretRepresentable(%q) = %v; want error containing %q", tt.raw, err, tt.wantErr)
			}
		})
	}
}

// TestSecretBytesRoundTrip proves that what the provider writes it reads
// back unchanged.
func TestSecretBytesRoundTrip(t *testing.T) {
	data := map[string]interface{}{"zip": "zap", "user": "alice"}
	raw, err := buildSecretBytes("s3cr3t", data)
	if err != nil {
		t.Fatalf("buildSecretBytes: %v", err)
	}

	got := parseSecretBytes(raw)
	if got.password != "s3cr3t" {
		t.Errorf("password = %q; want %q", got.password, "s3cr3t")
	}
	want := map[string]string{"zip": "zap", "user": "alice"}
	if !reflect.DeepEqual(got.data, want) {
		t.Errorf("data = %#v; want %#v", got.data, want)
	}
}
