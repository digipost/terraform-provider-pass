package pass

import (
	"reflect"
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
