package pass

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gopasspw/gopass/pkg/ctxutil"
	"github.com/gopasspw/gopass/pkg/gopass"
	"github.com/gopasspw/gopass/pkg/gopass/api"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/pkg/errors"
)

// passProvider is the configured provider: a git-synced store checkout and
// a gopass instance that encrypts/decrypts files inside it. All git
// operations go through store; gopass never commits or pushes (see
// docs/adr/0001-provider-owns-git-gopass-only-encrypts.md).
type passProvider struct {
	store *gitStore
	gp    *api.Gopass
}

func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"repo_url": {
				Type:          schema.TypeString,
				Optional:      true,
				ConflictsWith: []string{"store_dir"},
				Description:   "Git URL of the password store repository (e.g. `git@github.com:org/passwords.git`). The provider maintains its own clone of it in a per-repository store cache and pushes every write. Exactly one of `repo_url` and `store_dir` must be set.",
			},
			"branch": {
				Type:          schema.TypeString,
				Optional:      true,
				RequiredWith:  []string{"repo_url"},
				ConflictsWith: []string{"store_dir"},
				Description:   "Branch of `repo_url` to read and write. Defaults to the remote HEAD branch.",
			},
			"store_dir": {
				Type:          schema.TypeString,
				Optional:      true,
				ConflictsWith: []string{"repo_url", "branch"},
				Description:   "Existing local password store directory to use, e.g. a clone you manage yourself. Defaults to `$PASSWORD_STORE_DIR`. Must be checked out on a branch (not a detached HEAD); prefer `repo_url` and let the provider manage the checkout. Exactly one of `repo_url` and `store_dir` must be set.",
			},
			"refresh_store": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Whether to update the store from its git remote when the provider configures (fetch + reset for `repo_url` store caches, fetch + rebase for a `store_dir` clone). With `false` the provider works offline from the existing checkout: reads may be stale, but writes still push.",
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
	repoURL := d.Get("repo_url").(string)
	storeDir := d.Get("store_dir").(string)
	branch := d.Get("branch").(string)
	refresh := d.Get("refresh_store").(bool)

	if repoURL == "" && storeDir == "" {
		// Keep the long-standing pass convention as a fallback for
		// store_dir.
		storeDir = os.Getenv("PASSWORD_STORE_DIR")
	}

	var store *gitStore
	var err error
	switch {
	case repoURL != "" && storeDir != "":
		return nil, diag.Errorf("repo_url and store_dir are mutually exclusive: set exactly one")
	case repoURL != "":
		store, err = newCachedStore(ctx, repoURL, branch, refresh)
	case storeDir != "":
		store, err = newLocalStore(ctx, storeDir, refresh)
	default:
		return nil, diag.Errorf("exactly one of repo_url or store_dir must be set (store_dir also falls back to $PASSWORD_STORE_DIR)")
	}
	if err != nil {
		return nil, diag.FromErr(err)
	}

	// gopass discovers the store through this variable; it must be set
	// before api.New.
	os.Setenv("PASSWORD_STORE_DIR", store.dir)
	forceGopassGitConfig()

	gp, err := api.New(ctx)
	if err != nil {
		if errors.Is(err, api.ErrNotInitialized) {
			return nil, diag.Errorf("%s does not look like an initialized password store (no recipients file such as .gpg-id): %s", store.dir, err)
		}

		return nil, diag.FromErr(errors.Wrap(err, "error instantiating password store"))
	}

	return &passProvider{store: store, gp: gp}, nil
}

// forceGopassGitConfig turns off every gopass behavior that would touch git
// or the network on its own. This is defense in depth: writes already pass
// ctxutil.WithGitCommit(false), but the provider must hold even if gopass
// grows new call sites. Existing GOPASS_CONFIG_* entries are preserved;
// ours are appended after them.
func forceGopassGitConfig() {
	forced := [][2]string{
		{"core.autosync", "false"},
		{"core.autopush", "false"},
		{"core.exportkeys", "false"},
		{"core.notifications", "false"},
	}

	base, _ := strconv.Atoi(os.Getenv("GOPASS_CONFIG_COUNT"))
	for i, kv := range forced {
		os.Setenv(fmt.Sprintf("GOPASS_CONFIG_KEY_%d", base+i), kv[0])
		os.Setenv(fmt.Sprintf("GOPASS_CONFIG_VALUE_%d", base+i), kv[1])
	}
	os.Setenv("GOPASS_CONFIG_COUNT", strconv.Itoa(base+len(forced)))
}

// gopassWriteCtx marks a context so gopass encrypts and stages files but
// leaves committing and pushing to the provider's git layer.
func gopassWriteCtx(ctx context.Context) context.Context {
	return ctxutil.WithGitCommit(ctx, false)
}

// gopassReadCtx disables gopass' secret parsing so Get returns the exact
// on-disk plaintext; the provider does its own (v1-compatible) parsing.
func gopassReadCtx(ctx context.Context) context.Context {
	return ctxutil.WithShowParsing(ctx, false)
}

// isNotFoundErr matches gopass' "entry is not in the password store" error.
// The gopass API has no exported sentinel for it, so match on the message
// (pinned by TestNotFoundErrMessage).
func isNotFoundErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "entry is not in the password store")
}

// latestVersion is the gopass revision selector for "the current value of
// this secret". The provider only ever reads/writes the current revision.
const latestVersion = "latest"

// getSecret fetches the raw (unparsed) secret at path through the store's
// read lock, shared by the resource's Read, the data source's Read, and
// secretExists. Callers decide how to treat isNotFoundErr: some (e.g.
// resource Read) clear state on it, others (e.g. secretExists) treat it as
// "does not exist", and others (e.g. data source Read) surface it as a hard
// error.
func (pp *passProvider) getSecret(ctx context.Context, path string) (gopass.Secret, error) {
	var sec gopass.Secret
	err := pp.store.withRead(ctx, func() error {
		var err error
		sec, err = pp.gp.Get(gopassReadCtx(ctx), path, latestVersion)

		return err
	})

	return sec, err
}
