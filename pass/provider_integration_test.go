package pass

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

// These tests drive the full provider stack — configure, gopass encryption,
// and the git layer — against a local bare remote, using gopass' plaintext
// crypto backend (selected by the .plain-id file) so no GPG keyring is
// needed. The GPG path differs only in the crypto backend.

// isolateGopass points gopass' config and home discovery at throwaway
// directories on top of the git/cache isolation.
func isolateGopass(t *testing.T) {
	t.Helper()
	isolateGitAndCache(t)
	t.Setenv("GOPASS_HOMEDIR", t.TempDir())
	t.Setenv("GOPASS_CONFIG_COUNT", "")
	t.Setenv("PASSWORD_STORE_DIR", "")
}

// setupPlainStoreRemote seeds the bare remote with a plaintext-backend
// password store instead of the git-only fixture from setupRemote.
func setupPlainStoreRemote(t *testing.T) (remoteURL, otherClone string) {
	t.Helper()

	remoteURL = filepath.Join(t.TempDir(), "store.git")
	mustGit(t, t.TempDir(), "init", "--quiet", "--bare", "-b", "main", remoteURL)

	otherClone = filepath.Join(t.TempDir(), "other")
	mustGit(t, t.TempDir(), "clone", "--quiet", remoteURL, otherClone)
	writeFile(t, otherClone, ".plain-id", "test@example.com\n")
	mustGit(t, otherClone, "add", "--all")
	mustGit(t, otherClone, "commit", "--quiet", "-m", "seed store")
	mustGit(t, otherClone, "push", "--quiet", "origin", "main")

	return remoteURL, otherClone
}

// configureProvider runs the provider's ConfigureContextFunc with the given
// raw config and returns the resulting meta.
func configureProvider(t *testing.T, raw map[string]interface{}) (*passProvider, error) {
	t.Helper()

	p := Provider()
	diags := p.Configure(context.Background(), terraform.NewResourceConfigRaw(raw))
	if diags.HasError() {
		return nil, &testConfigureError{diags[0].Summary}
	}

	return p.Meta().(*passProvider), nil
}

// testConfigureError reports a Configure-time diagnostic as an error for
// tests. Deliberately distinct from git_store.go's configError (which marks
// provider-configuration mistakes for the store's own error handling) to
// avoid confusing the two same-package types.
type testConfigureError struct{ msg string }

func (e *testConfigureError) Error() string { return e.msg }

func resourceData(t *testing.T, raw map[string]interface{}) *schema.ResourceData {
	t.Helper()

	return schema.TestResourceDataRaw(t, passPasswordResource().Schema, raw)
}

func TestProviderEndToEnd(t *testing.T) {
	ctx := context.Background()

	t.Run("write, read, update, delete against a repo_url store", func(t *testing.T) {
		isolateGopass(t)
		remoteURL, other := setupPlainStoreRemote(t)

		pp, err := configureProvider(t, map[string]interface{}{"repo_url": remoteURL})
		if err != nil {
			t.Fatalf("configure: %v", err)
		}

		// Create.
		d := resourceData(t, map[string]interface{}{
			"path":     "secret/foo",
			"password": "0123456789",
			"data":     map[string]interface{}{"zip": "zap"},
		})
		if diags := passPasswordResourceCreate(ctx, d, pp); diags.HasError() {
			t.Fatalf("create: %v", diags)
		}
		if d.Id() != "secret/foo" {
			t.Errorf("id = %q; want secret/foo", d.Id())
		}

		// The write must be on the remote, as one commit with the exact
		// message, and byte-identical to the v1.9.2 format. The plaintext
		// backend stores the plaintext verbatim, so the file content IS
		// what gopass encrypted.
		mustGit(t, other, "pull", "--quiet", "--rebase", "origin", "main")
		if got := mustGit(t, other, "log", "-1", "--format=%s"); got != "terraform-provider-pass: write secret/foo" {
			t.Errorf("remote tip message = %q", got)
		}
		onDisk, err := os.ReadFile(filepath.Join(other, "secret", "foo.txt"))
		if err != nil {
			t.Fatalf("secret missing in human clone: %v", err)
		}
		if want := "0123456789\n---\nzip: zap\n"; string(onDisk) != want {
			t.Errorf("on-disk plaintext = %q; want %q (v1.9.2-compatible format)", onDisk, want)
		}

		// gopass must not have produced its own commits ("Save secret...")
		// and nothing may be left uncommitted or unpushed: the provider
		// owns git entirely, and with the noop queue everything is done
		// synchronously by the time the write returns.
		if msgs := mustGit(t, other, "log", "--format=%s"); strings.Contains(msgs, "Save secret") {
			t.Errorf("gopass created its own commits:\n%s", msgs)
		}
		if out := mustGit(t, pp.store.dir, "status", "--porcelain"); out != "" {
			t.Errorf("store cache dirty after write:\n%s", out)
		}
		if ahead := mustGit(t, pp.store.dir, "rev-list", "--count", "origin/main..HEAD"); ahead != "0" {
			t.Errorf("store cache is %s commits ahead of origin after write; the push must be synchronous", ahead)
		}

		// Read back through the resource.
		rd := resourceData(t, map[string]interface{}{})
		rd.SetId("secret/foo")
		if diags := passPasswordResourceRead(ctx, rd, pp); diags.HasError() {
			t.Fatalf("read: %v", diags)
		}
		if got := rd.Get("password").(string); got != "0123456789" {
			t.Errorf("read password = %q", got)
		}
		if got := rd.Get("data").(map[string]interface{})["zip"]; got != "zap" {
			t.Errorf("read data.zip = %v", got)
		}

		// Update.
		d = resourceData(t, map[string]interface{}{
			"path":     "secret/foo",
			"password": "updated",
		})
		d.SetId("secret/foo")
		if diags := passPasswordResourceWrite(ctx, d, pp); diags.HasError() {
			t.Fatalf("update: %v", diags)
		}
		mustGit(t, other, "pull", "--quiet", "--rebase", "origin", "main")
		onDisk, err = os.ReadFile(filepath.Join(other, "secret", "foo.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(onDisk) != "updated" {
			t.Errorf("updated plaintext = %q; want %q with no trailing newline", onDisk, "updated")
		}

		// Delete.
		dd := resourceData(t, map[string]interface{}{})
		dd.SetId("secret/foo")
		if diags := passPasswordResourceDelete(ctx, dd, pp); diags.HasError() {
			t.Fatalf("delete: %v", diags)
		}
		mustGit(t, other, "pull", "--quiet", "--rebase", "origin", "main")
		if got := mustGit(t, other, "log", "-1", "--format=%s"); got != "terraform-provider-pass: delete secret/foo" {
			t.Errorf("delete commit message = %q", got)
		}
		if _, err := os.Stat(filepath.Join(other, "secret", "foo.txt")); err == nil {
			t.Error("secret still present on the remote after delete")
		}
	})

	t.Run("reads a secret written by the v1.9.2 provider byte-for-byte", func(t *testing.T) {
		isolateGopass(t)
		remoteURL, other := setupPlainStoreRemote(t)

		// Exact bytes the old provider (gopass v1.9.2 secret.New) wrote.
		writeFile(t, other, "legacy/db.txt", "sup3rs3cret\n---\nhost: db.example.com\nuser: app\n")
		mustGit(t, other, "add", "--all")
		mustGit(t, other, "commit", "--quiet", "-m", "written by v1 provider")
		mustGit(t, other, "push", "--quiet", "origin", "main")

		pp, err := configureProvider(t, map[string]interface{}{"repo_url": remoteURL})
		if err != nil {
			t.Fatalf("configure: %v", err)
		}

		ds := schema.TestResourceDataRaw(t, passwordDataSource().Schema, map[string]interface{}{"path": "legacy/db"})
		if diags := passwordDataSourceRead(ctx, ds, pp); diags.HasError() {
			t.Fatalf("data source read: %v", diags)
		}
		if got := ds.Get("password").(string); got != "sup3rs3cret" {
			t.Errorf("password = %q", got)
		}
		data := ds.Get("data").(map[string]interface{})
		if data["host"] != "db.example.com" || data["user"] != "app" {
			t.Errorf("data = %#v", data)
		}
		if got := ds.Get("body").(string); got != "---\nhost: db.example.com\nuser: app\n" {
			t.Errorf("body = %q", got)
		}
		if got := ds.Get("full").(string); got != "sup3rs3cret\n---\nhost: db.example.com\nuser: app\n" {
			t.Errorf("full = %q", got)
		}
	})

	t.Run("resource read of a secret deleted by a human clears the id", func(t *testing.T) {
		isolateGopass(t)
		remoteURL, _ := setupPlainStoreRemote(t)

		pp, err := configureProvider(t, map[string]interface{}{"repo_url": remoteURL})
		if err != nil {
			t.Fatalf("configure: %v", err)
		}

		rd := resourceData(t, map[string]interface{}{})
		rd.SetId("never/written")
		if diags := passPasswordResourceRead(ctx, rd, pp); diags.HasError() {
			t.Fatalf("read of missing secret must not error: %v", diags)
		}
		if rd.Id() != "" {
			t.Errorf("id = %q; want empty so terraform recreates the resource", rd.Id())
		}
	})

	t.Run("deleting an already-deleted secret succeeds", func(t *testing.T) {
		isolateGopass(t)
		remoteURL, _ := setupPlainStoreRemote(t)

		pp, err := configureProvider(t, map[string]interface{}{"repo_url": remoteURL})
		if err != nil {
			t.Fatalf("configure: %v", err)
		}

		dd := resourceData(t, map[string]interface{}{})
		dd.SetId("never/written")
		if diags := passPasswordResourceDelete(ctx, dd, pp); diags.HasError() {
			t.Errorf("delete of missing secret must be idempotent: %v", diags)
		}
	})

	t.Run("store_dir mode still works against a real clone", func(t *testing.T) {
		isolateGopass(t)
		remoteURL, other := setupPlainStoreRemote(t)

		dir := filepath.Join(t.TempDir(), "clone")
		mustGit(t, t.TempDir(), "clone", "--quiet", remoteURL, dir)

		pp, err := configureProvider(t, map[string]interface{}{"store_dir": dir})
		if err != nil {
			t.Fatalf("configure: %v", err)
		}

		d := resourceData(t, map[string]interface{}{
			"path":     "secret/local",
			"password": "pw",
		})
		if diags := passPasswordResourceCreate(ctx, d, pp); diags.HasError() {
			t.Fatalf("create: %v", diags)
		}
		mustGit(t, other, "pull", "--quiet", "--rebase", "origin", "main")
		if _, err := os.Stat(filepath.Join(other, "secret", "local.txt")); err != nil {
			t.Errorf("store_dir write did not reach the remote: %v", err)
		}
	})

	t.Run("create refuses to overwrite a secret not tracked in state", func(t *testing.T) {
		isolateGopass(t)
		remoteURL, other := setupPlainStoreRemote(t)

		// Seed a secret directly, as if a human or another tool wrote it;
		// terraform has no state entry for it.
		writeFile(t, other, "shared/api_key.txt", "human-written-secret")
		mustGit(t, other, "add", "--all")
		mustGit(t, other, "commit", "--quiet", "-m", "written by a human")
		mustGit(t, other, "push", "--quiet", "origin", "main")

		pp, err := configureProvider(t, map[string]interface{}{"repo_url": remoteURL})
		if err != nil {
			t.Fatalf("configure: %v", err)
		}

		d := resourceData(t, map[string]interface{}{
			"path":     "shared/api_key",
			"password": "terraform-would-overwrite-this",
		})
		diags := passPasswordResourceCreate(ctx, d, pp)
		if !diags.HasError() {
			t.Fatal("create must fail when a secret already exists at the path")
		}
		if !strings.Contains(diags[0].Summary, "terraform import") {
			t.Errorf("error must point at terraform import, got %q", diags[0].Summary)
		}
		if d.Id() != "" {
			t.Errorf("id = %q; want empty, resource must not be adopted implicitly", d.Id())
		}

		// The secret on the remote must be untouched.
		mustGit(t, other, "pull", "--quiet", "--rebase", "origin", "main")
		onDisk, err := os.ReadFile(filepath.Join(other, "shared", "api_key.txt"))
		if err != nil {
			t.Fatalf("secret disappeared from the remote: %v", err)
		}
		if string(onDisk) != "human-written-secret" {
			t.Errorf("secret was overwritten: on-disk = %q", onDisk)
		}
		if got := mustGit(t, other, "log", "-1", "--format=%s"); got != "written by a human" {
			t.Errorf("an unexpected commit was pushed: %q", got)
		}
	})

	t.Run("reading a freshly imported secret populates path, password and data", func(t *testing.T) {
		isolateGopass(t)
		remoteURL, other := setupPlainStoreRemote(t)

		// A secret that pre-dates any terraform state, exactly like the
		// scenario `terraform import` exists for.
		writeFile(t, other, "shared/imported.txt", "s3cr3t\n---\nzip: zap\n")
		mustGit(t, other, "add", "--all")
		mustGit(t, other, "commit", "--quiet", "-m", "written before import")
		mustGit(t, other, "push", "--quiet", "origin", "main")

		pp, err := configureProvider(t, map[string]interface{}{"repo_url": remoteURL})
		if err != nil {
			t.Fatalf("configure: %v", err)
		}

		// ImportStatePassthroughContext hands Read a ResourceData with
		// only the ID set; nothing else is populated ahead of Read.
		rd := resourceData(t, map[string]interface{}{})
		rd.SetId("shared/imported")
		if diags := passPasswordResourceRead(ctx, rd, pp); diags.HasError() {
			t.Fatalf("read: %v", diags)
		}
		if got := rd.Get("path").(string); got != "shared/imported" {
			t.Errorf("path = %q; want shared/imported (path is Required+ForceNew, so import leaves a forced replace unless Read sets it)", got)
		}
		if got := rd.Get("password").(string); got != "s3cr3t" {
			t.Errorf("password = %q", got)
		}
		if got := rd.Get("data").(map[string]interface{})["zip"]; got != "zap" {
			t.Errorf("data.zip = %v", got)
		}
	})
}

func TestProviderConfigure(t *testing.T) {
	t.Run("both repo_url and store_dir is an error", func(t *testing.T) {
		isolateGopass(t)

		_, err := configureProvider(t, map[string]interface{}{
			"repo_url":  "ssh://git@example.com/store.git",
			"store_dir": t.TempDir(),
		})
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("want mutual-exclusivity error, got %v", err)
		}
	})

	t.Run("neither repo_url nor store_dir is an error", func(t *testing.T) {
		isolateGopass(t)

		_, err := configureProvider(t, map[string]interface{}{})
		if err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Errorf("want exactly-one error, got %v", err)
		}
	})

	t.Run("PASSWORD_STORE_DIR fills in store_dir", func(t *testing.T) {
		isolateGopass(t)
		remoteURL, _ := setupPlainStoreRemote(t)
		dir := filepath.Join(t.TempDir(), "clone")
		mustGit(t, t.TempDir(), "clone", "--quiet", remoteURL, dir)
		t.Setenv("PASSWORD_STORE_DIR", dir)

		if _, err := configureProvider(t, map[string]interface{}{}); err != nil {
			t.Errorf("configure via PASSWORD_STORE_DIR: %v", err)
		}
	})

	t.Run("detached HEAD store_dir fails with a pointer to repo_url", func(t *testing.T) {
		isolateGopass(t)
		remoteURL, _ := setupPlainStoreRemote(t)
		dir := filepath.Join(t.TempDir(), "modclone")
		mustGit(t, t.TempDir(), "clone", "--quiet", remoteURL, dir)
		mustGit(t, dir, "checkout", "--quiet", "--detach", "HEAD")

		_, err := configureProvider(t, map[string]interface{}{"store_dir": dir})
		if err == nil || !strings.Contains(err.Error(), "repo_url") {
			t.Errorf("want detached-HEAD error pointing at repo_url, got %v", err)
		}
	})

	t.Run("uninitialized store is a clear error", func(t *testing.T) {
		isolateGopass(t)
		// A git-less, recipient-less directory.
		_, err := configureProvider(t, map[string]interface{}{
			"store_dir":     t.TempDir(),
			"refresh_store": false,
		})
		if err == nil || !strings.Contains(err.Error(), "initialized") {
			t.Errorf("want not-initialized error, got %v", err)
		}
	})
}

// TestNotFoundErrMessage pins the gopass error text isNotFoundErr matches
// on. If a gopass upgrade changes the message, this fails before any user
// sees resources silently misbehave.
func TestNotFoundErrMessage(t *testing.T) {
	ctx := context.Background()
	isolateGopass(t)
	remoteURL, _ := setupPlainStoreRemote(t)

	pp, err := configureProvider(t, map[string]interface{}{"repo_url": remoteURL})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}

	_, err = pp.gp.Get(gopassReadCtx(ctx), "no/such/secret", "latest")
	if err == nil {
		t.Fatal("expected an error for a missing secret")
	}
	if !isNotFoundErr(err) {
		t.Errorf("isNotFoundErr does not match gopass' actual error %q; update the matcher", err)
	}
}
