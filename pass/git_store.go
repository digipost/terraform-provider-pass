package pass

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/pkg/errors"
)

const remoteName = "origin"

// pushAttempts is the total number of pushes tried per write: the initial
// push plus up to 3 fetch/rebase/push retries after a rejection.
const pushAttempts = 4

// flockRetryDelay is how often a blocked flock acquisition re-tries.
const flockRetryDelay = 250 * time.Millisecond

// gitStore mediates every git interaction with a password store checkout.
// gopass only encrypts and stages files; clone, fetch, rebase, commit and
// push are all done here by shelling out to git.
type gitStore struct {
	// dir is the store's working tree, exposed to gopass via
	// PASSWORD_STORE_DIR.
	dir string
	// branch to sync with origin; empty when syncing is off.
	branch string
	// syncing: fetch/rebase before writes, push after. Requires an origin
	// remote.
	syncing bool
	// isGit: dir is a git worktree. Local commits are created on write even
	// when syncing is off (e.g. a store_dir clone without an origin remote).
	isGit bool
	// cache: the dir is a provider-owned store cache, which is disposable —
	// hard resets and nuke-and-reclone are allowed. Never set for an
	// operator-provided store_dir.
	cache bool
	// fl guards the checkout against other terraform-provider-pass
	// processes. Nil when there is nowhere sane to put a lock file (plain
	// non-git store_dir).
	fl *flock.Flock
	mu sync.Mutex
}

// configError marks provider-configuration mistakes that nuking the store
// cache cannot fix (e.g. a branch that does not exist on the remote).
type configError struct{ err error }

func (e configError) Error() string { return e.err.Error() }

func isConfigError(err error) bool {
	var ce configError
	return errors.As(err, &ce)
}

// runGit executes git with the given arguments in dir, returning trimmed
// combined output. Errors include the full git output — that output is what
// makes git failures actionable.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Never let git sit waiting for credentials on a terminal that isn't
	// there.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}

	return strings.TrimSpace(string(out)), nil
}

// storeCachePaths returns the store cache checkout dir and its lock file
// for a repo URL. The lock file lives next to the checkout, not inside it,
// so it survives nuke-and-reclone.
func storeCachePaths(repoURL string) (dir, lockPath string, err error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", "", errors.Wrap(err, "cannot determine user cache directory for the store cache")
	}

	root := filepath.Join(base, "terraform-provider-pass")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", "", errors.Wrapf(err, "cannot create store cache root %s", root)
	}

	sum := sha256.Sum256([]byte(repoURL))
	name := hex.EncodeToString(sum[:])[:16]

	return filepath.Join(root, name), filepath.Join(root, name+".lock"), nil
}

// newCachedStore clones or refreshes the provider-managed store cache for
// repoURL and returns a syncing gitStore on the requested branch (or the
// remote HEAD branch when branch is empty). Any inconsistent git state in
// the cache is repaired by deleting it and cloning fresh — the cache is
// disposable by design.
func newCachedStore(ctx context.Context, repoURL, branch string, refresh bool) (*gitStore, error) {
	dir, lockPath, err := storeCachePaths(repoURL)
	if err != nil {
		return nil, err
	}

	s := &gitStore{
		dir:     dir,
		syncing: true,
		isGit:   true,
		cache:   true,
		fl:      flock.New(lockPath),
	}

	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.unlock()

	if s.cacheUsable(ctx, repoURL) {
		if refresh {
			err = s.refreshCache(ctx, branch)
		} else {
			err = s.ensureBranchOffline(ctx, branch)
		}
		if err == nil {
			return s, nil
		}
		if isConfigError(err) {
			return nil, err
		}
		tflog.Warn(ctx, fmt.Sprintf("store cache %s is unusable (%v); deleting it and cloning fresh", dir, err))
	}

	if err := s.clone(ctx, repoURL, branch); err != nil {
		return nil, err
	}

	return s, nil
}

// cacheUsable reports whether dir holds a healthy checkout of repoURL.
func (s *gitStore) cacheUsable(ctx context.Context, repoURL string) bool {
	if _, err := os.Stat(filepath.Join(s.dir, ".git")); err != nil {
		return false
	}
	if _, err := runGit(ctx, s.dir, "rev-parse", "--is-inside-work-tree"); err != nil {
		return false
	}
	url, err := runGit(ctx, s.dir, "remote", "get-url", remoteName)

	return err == nil && url == repoURL
}

// clone nukes the cache dir and clones repoURL into it.
func (s *gitStore) clone(ctx context.Context, repoURL, branch string) error {
	if err := os.RemoveAll(s.dir); err != nil {
		return errors.Wrapf(err, "cannot remove stale store cache %s", s.dir)
	}

	args := []string{"clone", "--quiet"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, "--", repoURL, s.dir)

	if _, err := runGit(ctx, filepath.Dir(s.dir), args...); err != nil {
		return errors.Wrapf(err, "cloning password store %s into the store cache failed", repoURL)
	}

	cur, err := s.currentBranch(ctx)
	if err != nil {
		return errors.Wrap(err, "freshly cloned store cache has no current branch")
	}
	s.branch = cur

	return nil
}

// refreshCache brings an existing cache up to date with origin: fetch, then
// force the requested branch (or the currently checked out one) to exactly
// match its remote counterpart. Local commits and untracked files are
// discarded — they can only be leftovers from failed runs.
func (s *gitStore) refreshCache(ctx context.Context, branch string) error {
	if _, err := runGit(ctx, s.dir, "fetch", "--prune", "--quiet", remoteName); err != nil {
		return err
	}

	if branch == "" {
		cur, err := s.currentBranch(ctx)
		if err != nil {
			return err
		}
		branch = cur
	}

	if !s.remoteRefExists(ctx, branch) {
		return configError{fmt.Errorf("branch %q does not exist on the password store remote", branch)}
	}

	if _, err := runGit(ctx, s.dir, "checkout", "--quiet", "-B", branch, remoteName+"/"+branch); err != nil {
		return err
	}
	if _, err := runGit(ctx, s.dir, "clean", "-ffdq"); err != nil {
		return err
	}
	s.branch = branch

	return nil
}

// ensureBranchOffline positions an existing cache on the requested branch
// without touching the network (refresh_store = false: stale reads are
// acceptable).
func (s *gitStore) ensureBranchOffline(ctx context.Context, branch string) error {
	cur, err := s.currentBranch(ctx)
	if err != nil {
		return err
	}
	if branch == "" || branch == cur {
		s.branch = cur

		return nil
	}

	// Best effort from refs fetched on a previous run; failure falls back
	// to a fresh clone of the requested branch.
	if _, err := runGit(ctx, s.dir, "checkout", "--quiet", "-B", branch, remoteName+"/"+branch); err != nil {
		return err
	}
	s.branch = branch

	return nil
}

// newLocalStore wraps an operator-managed store_dir. The provider is a
// guest here: it never hard-resets, cleans, or deletes anything, and
// refresh/conflict failures surface as errors instead of repairs.
func newLocalStore(ctx context.Context, dir string, refresh bool) (*gitStore, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, errors.Wrapf(err, "store_dir %s is not usable", dir)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("store_dir %s is not a directory", dir)
	}

	s := &gitStore{dir: dir}

	// The store counts as a git repository only when it is the repository's
	// toplevel. A plain store directory that merely sits inside some other
	// repository must not make the provider stage and commit that
	// repository's files.
	topLevel, topErr := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if topErr != nil || !sameDir(dir, topLevel) {
		// A plain directory without git. Reads and writes work on files
		// only; there is nothing to pull or push.
		if refresh {
			return nil, fmt.Errorf("store_dir %s is not a git repository, so refresh_store cannot pull updates: set refresh_store = false for a purely local store, or switch to repo_url and let the provider manage the checkout", dir)
		}

		return s, nil
	}

	gitDir, err := runGit(ctx, dir, "rev-parse", "--git-dir")
	if err != nil {
		return nil, errors.Wrapf(err, "cannot locate the git dir of store_dir %s", dir)
	}
	s.isGit = true
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	s.fl = flock.New(filepath.Join(gitDir, "terraform-provider-pass.lock"))

	branch, err := s.currentBranch(ctx)
	if err != nil {
		return nil, fmt.Errorf("store_dir %s is on a detached HEAD, so there is no branch to push to (this is what .terraform/modules checkouts look like): point the provider at the password store with repo_url instead of store_dir and it will manage its own clone", dir)
	}
	s.branch = branch

	if _, err := runGit(ctx, dir, "remote", "get-url", remoteName); err != nil {
		// A local-only repository: commit on write, nothing to sync with.
		if refresh {
			return nil, fmt.Errorf("store_dir %s has no %q remote, so refresh_store cannot pull updates: set refresh_store = false or use repo_url", dir, remoteName)
		}

		return s, nil
	}
	s.syncing = true

	if refresh {
		if err := s.lock(ctx); err != nil {
			return nil, err
		}
		defer s.unlock()

		if _, err := runGit(ctx, dir, "fetch", "--prune", "--quiet", remoteName); err != nil {
			return nil, errors.Wrap(err, "refreshing the password store failed")
		}
		if s.remoteRefExists(ctx, branch) {
			if _, err := runGit(ctx, dir, "rebase", remoteName+"/"+branch); err != nil {
				_, _ = runGit(ctx, dir, "rebase", "--abort")

				return nil, errors.Wrapf(err, "rebasing store_dir %s onto %s/%s failed; resolve the repository state manually", dir, remoteName, branch)
			}
		}
	}

	return s, nil
}

// withRead runs fn while holding a shared cross-process lock, so a
// concurrent terraform run cannot reclone or reset the checkout mid-read.
func (s *gitStore) withRead(ctx context.Context, fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fl != nil {
		ok, err := s.fl.TryRLockContext(ctx, flockRetryDelay)
		if err != nil || !ok {
			return errors.Wrapf(err, "cannot lock password store checkout %s", s.dir)
		}
		defer s.unlock()
	}

	return fn()
}

// withWrite runs mutate (a gopass write, which encrypts and stages files)
// wrapped in the full write protocol: fetch → rebase onto origin/<branch> →
// mutate → commit → push, retrying rejected pushes up to 3 times. A rebase
// conflict means someone changed the same secret concurrently; that always
// fails loudly rather than picking a winner.
func (s *gitStore) withWrite(ctx context.Context, commitMsg string, mutate func(context.Context) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fl != nil {
		ok, err := s.fl.TryLockContext(ctx, flockRetryDelay)
		if err != nil || !ok {
			return errors.Wrapf(err, "cannot lock password store checkout %s", s.dir)
		}
		defer s.unlock()
	}

	if s.syncing {
		if _, err := runGit(ctx, s.dir, "fetch", "--quiet", remoteName); err != nil {
			return errors.Wrap(err, "fetching the password store remote before writing failed")
		}
		if err := s.rebaseOntoRemote(ctx); err != nil {
			return err
		}
	}

	if err := mutate(ctx); err != nil {
		s.discardCacheChanges(ctx)

		return err
	}

	if !s.isGit {
		return nil
	}

	if _, err := runGit(ctx, s.dir, "add", "--all"); err != nil {
		return errors.Wrap(err, "staging password store changes failed")
	}
	staged, err := runGit(ctx, s.dir, "status", "--porcelain")
	if err != nil {
		return errors.Wrap(err, "checking password store status failed")
	}
	if staged == "" {
		// Nothing changed on disk, nothing to commit or push.
		return nil
	}

	if _, err := runGit(ctx, s.dir, "commit", "--quiet", "-m", commitMsg); err != nil {
		s.discardCacheChanges(ctx)
		if strings.Contains(err.Error(), "Please tell me who you are") {
			return errors.Wrap(err, "committing to the password store failed because git has no identity; configure user.name and user.email for the user running terraform (CI jobs included)")
		}

		return errors.Wrap(err, "committing to the password store failed")
	}

	if !s.syncing {
		return nil
	}

	var pushErr error
	for attempt := 1; attempt <= pushAttempts; attempt++ {
		if _, pushErr = runGit(ctx, s.dir, "push", "--quiet", remoteName, "HEAD:refs/heads/"+s.branch); pushErr == nil {
			return nil
		}

		tflog.Warn(ctx, fmt.Sprintf("push to %s/%s rejected (attempt %d of %d): %v", remoteName, s.branch, attempt, pushAttempts, pushErr))

		if attempt == pushAttempts {
			break
		}
		if _, err := runGit(ctx, s.dir, "fetch", "--quiet", remoteName); err != nil {
			s.discardCacheChanges(ctx)

			return errors.Wrap(err, "fetching the password store remote while retrying a rejected push failed")
		}
		if err := s.rebaseOntoRemote(ctx); err != nil {
			return err
		}
	}

	s.discardCacheChanges(ctx)

	return errors.Wrapf(pushErr, "pushing to the password store was rejected %d times, the remote is receiving concurrent writes; re-run terraform", pushAttempts)
}

// rebaseOntoRemote rebases the current branch onto origin/<branch>. On
// conflict the rebase is aborted and the error tells the operator exactly
// which secrets collided.
func (s *gitStore) rebaseOntoRemote(ctx context.Context) error {
	if !s.remoteRefExists(ctx, s.branch) {
		// A yet-unborn remote branch (empty repository): the push will
		// create it.
		return nil
	}

	if _, err := runGit(ctx, s.dir, "rebase", remoteName+"/"+s.branch); err != nil {
		_, _ = runGit(ctx, s.dir, "rebase", "--abort")
		s.discardCacheChanges(ctx)

		return errors.Wrapf(err, "the password store received a conflicting concurrent change (rebase onto %s/%s failed); the provider never auto-resolves conflicting secret writes — inspect the store and re-run terraform", remoteName, s.branch)
	}

	return nil
}

// discardCacheChanges restores a pristine checkout after a failed write.
// Only the provider-owned cache is ever reset; an operator's store_dir is
// left exactly as the failure left it, for manual inspection.
func (s *gitStore) discardCacheChanges(ctx context.Context) {
	if !s.cache {
		return
	}
	if s.remoteRefExists(ctx, s.branch) {
		_, _ = runGit(ctx, s.dir, "reset", "--hard", "--quiet", remoteName+"/"+s.branch)
	}
	_, _ = runGit(ctx, s.dir, "clean", "-ffdq")
}

// sameDir reports whether two paths name the same directory once made
// absolute and resolved through symlinks.
func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	ra, err = filepath.Abs(ra)
	if err != nil {
		return false
	}
	rb, err = filepath.Abs(rb)
	if err != nil {
		return false
	}

	return ra == rb
}

func (s *gitStore) currentBranch(ctx context.Context) (string, error) {
	return runGit(ctx, s.dir, "symbolic-ref", "--quiet", "--short", "HEAD")
}

func (s *gitStore) remoteRefExists(ctx context.Context, branch string) bool {
	_, err := runGit(ctx, s.dir, "rev-parse", "--quiet", "--verify", remoteName+"/"+branch)

	return err == nil
}

func (s *gitStore) lock(ctx context.Context) error {
	if s.fl == nil {
		return nil
	}
	ok, err := s.fl.TryLockContext(ctx, flockRetryDelay)
	if err != nil || !ok {
		return errors.Wrapf(err, "cannot lock password store checkout %s", s.dir)
	}

	return nil
}

func (s *gitStore) unlock() {
	if s.fl != nil {
		_ = s.fl.Unlock()
	}
}
