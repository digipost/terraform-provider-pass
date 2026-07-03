package pass

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestProviderEndToEndGPG exercises the same full stack as
// TestProviderEndToEnd but with the real GPG crypto backend, proving the
// provider round-trips production-shaped stores. Skipped when no gpg binary
// is available.
func TestProviderEndToEndGPG(t *testing.T) {
	if _, err := exec.LookPath("gpg"); err != nil {
		t.Skip("gpg not installed")
	}

	ctx := context.Background()
	isolateGopass(t)

	// A throwaway keyring with one passphrase-less key.
	gnupgHome := t.TempDir()
	if err := os.Chmod(gnupgHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GNUPGHOME", gnupgHome)
	gpgCmd := exec.Command("gpg", "--batch", "--passphrase", "", "--quick-gen-key", "Test <test@example.com>", "default", "default", "never")
	if out, err := gpgCmd.CombinedOutput(); err != nil {
		t.Fatalf("generating gpg key: %v\n%s", err, out)
	}

	// A bare remote seeded as a GPG password store.
	remoteURL := filepath.Join(t.TempDir(), "store.git")
	mustGit(t, t.TempDir(), "init", "--quiet", "--bare", "-b", "main", remoteURL)
	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, t.TempDir(), "clone", "--quiet", remoteURL, other)
	writeFile(t, other, ".gpg-id", "test@example.com\n")
	mustGit(t, other, "add", "--all")
	mustGit(t, other, "commit", "--quiet", "-m", "seed store")
	mustGit(t, other, "push", "--quiet", "origin", "main")

	pp, err := configureProvider(t, map[string]interface{}{"repo_url": remoteURL})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}

	d := resourceData(t, map[string]interface{}{
		"path":     "secret/gpg",
		"password": "s3cr3t-gpg",
		"data":     map[string]interface{}{"zip": "zap"},
	})
	if diags := passPasswordResourceWrite(ctx, d, pp); diags.HasError() {
		t.Fatalf("write: %v", diags)
	}

	// The pushed file must be GPG ciphertext, not plaintext.
	mustGit(t, other, "pull", "--quiet", "--rebase", "origin", "main")
	ciphertext, err := os.ReadFile(filepath.Join(other, "secret", "gpg.gpg"))
	if err != nil {
		t.Fatalf("encrypted secret missing in human clone: %v", err)
	}
	if strings.Contains(string(ciphertext), "s3cr3t-gpg") {
		t.Fatal("secret file contains the plaintext password")
	}

	// Decrypting with plain gpg — what a human's pass CLI does — must give
	// the exact v1.9.2-compatible plaintext.
	decrypt := exec.Command("gpg", "--batch", "--quiet", "--decrypt", filepath.Join(other, "secret", "gpg.gpg"))
	plaintext, err := decrypt.Output()
	if err != nil {
		t.Fatalf("gpg decrypt: %v", err)
	}
	if want := "s3cr3t-gpg\n---\nzip: zap\n"; string(plaintext) != want {
		t.Errorf("decrypted plaintext = %q; want %q", plaintext, want)
	}

	// And the provider reads back what it wrote.
	rd := resourceData(t, map[string]interface{}{})
	rd.SetId("secret/gpg")
	if diags := passPasswordResourceRead(ctx, rd, pp); diags.HasError() {
		t.Fatalf("read: %v", diags)
	}
	if got := rd.Get("password").(string); got != "s3cr3t-gpg" {
		t.Errorf("read password = %q", got)
	}
}
