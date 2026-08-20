package pass

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustGit runs git in dir and fails the test on error.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %s in %s: %v", strings.Join(args, " "), dir, err)
	}

	return out
}

// isolateGitAndCache gives every test its own git identity/config and its
// own store cache root, so tests neither read the operator's config nor
// litter the real cache.
func isolateGitAndCache(t *testing.T) {
	t.Helper()

	gitConfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitConfig, []byte("[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitConfig)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
}

// setupRemote creates a bare "password store" remote seeded with one commit
// on main, and returns its URL plus a working clone the test can use to act
// as "the other writer".
func setupRemote(t *testing.T) (remoteURL, otherClone string) {
	t.Helper()

	remoteURL = filepath.Join(t.TempDir(), "store.git")
	mustGit(t, t.TempDir(), "init", "--quiet", "--bare", "-b", "main", remoteURL)

	otherClone = filepath.Join(t.TempDir(), "other")
	mustGit(t, t.TempDir(), "clone", "--quiet", remoteURL, otherClone)
	writeFile(t, otherClone, ".gpg-id", "test@example.com\n")
	mustGit(t, otherClone, "add", "--all")
	mustGit(t, otherClone, "commit", "--quiet", "-m", "seed store")
	mustGit(t, otherClone, "push", "--quiet", "origin", "main")

	return remoteURL, otherClone
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// pushFromOther commits a file in the second clone and pushes it, simulating
// a human writing to the store concurrently.
func pushFromOther(t *testing.T, otherClone, name, content, msg string) {
	t.Helper()
	mustGit(t, otherClone, "pull", "--quiet", "--rebase", "origin", "main")
	writeFile(t, otherClone, name, content)
	mustGit(t, otherClone, "add", "--all")
	mustGit(t, otherClone, "commit", "--quiet", "-m", msg)
	mustGit(t, otherClone, "push", "--quiet", "origin", "main")
}

func TestNewCachedStore(t *testing.T) {
	ctx := context.Background()

	t.Run("clones on first use onto the remote HEAD branch", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, _ := setupRemote(t)

		s, err := newCachedStore(ctx, remoteURL, "", true)
		if err != nil {
			t.Fatalf("newCachedStore: %v", err)
		}
		if s.branch != "main" {
			t.Errorf("branch = %q; want main", s.branch)
		}
		if _, err := os.Stat(filepath.Join(s.dir, ".gpg-id")); err != nil {
			t.Errorf("store cache is missing the seeded store content: %v", err)
		}
		if !strings.Contains(s.dir, "terraform-provider-pass") {
			t.Errorf("cache dir %q not under terraform-provider-pass cache root", s.dir)
		}
	})

	t.Run("refresh pulls new remote commits", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, other := setupRemote(t)

		if _, err := newCachedStore(ctx, remoteURL, "", true); err != nil {
			t.Fatalf("first newCachedStore: %v", err)
		}
		pushFromOther(t, other, "new-secret.gpg", "ciphertext", "add secret")

		s, err := newCachedStore(ctx, remoteURL, "", true)
		if err != nil {
			t.Fatalf("second newCachedStore: %v", err)
		}
		if _, err := os.Stat(filepath.Join(s.dir, "new-secret.gpg")); err != nil {
			t.Errorf("refresh did not pull the new commit: %v", err)
		}
	})

	t.Run("refresh false leaves the cache stale", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, other := setupRemote(t)

		if _, err := newCachedStore(ctx, remoteURL, "", true); err != nil {
			t.Fatalf("first newCachedStore: %v", err)
		}
		pushFromOther(t, other, "new-secret.gpg", "ciphertext", "add secret")

		s, err := newCachedStore(ctx, remoteURL, "", false)
		if err != nil {
			t.Fatalf("offline newCachedStore: %v", err)
		}
		if _, err := os.Stat(filepath.Join(s.dir, "new-secret.gpg")); err == nil {
			t.Error("offline configure unexpectedly fetched new commits")
		}
	})

	t.Run("refresh discards local commits and untracked files", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, _ := setupRemote(t)

		s, err := newCachedStore(ctx, remoteURL, "", true)
		if err != nil {
			t.Fatalf("first newCachedStore: %v", err)
		}
		// Leftovers from a hypothetical failed run: a stray commit and an
		// untracked file.
		writeFile(t, s.dir, "stray.gpg", "junk")
		mustGit(t, s.dir, "add", "--all")
		mustGit(t, s.dir, "commit", "--quiet", "-m", "stray")
		writeFile(t, s.dir, "untracked.tmp", "junk")

		s, err = newCachedStore(ctx, remoteURL, "", true)
		if err != nil {
			t.Fatalf("second newCachedStore: %v", err)
		}
		if _, err := os.Stat(filepath.Join(s.dir, "stray.gpg")); err == nil {
			t.Error("refresh kept a stray local commit")
		}
		if _, err := os.Stat(filepath.Join(s.dir, "untracked.tmp")); err == nil {
			t.Error("refresh kept an untracked file")
		}
	})

	t.Run("nukes and reclones an inconsistent cache", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, _ := setupRemote(t)

		s, err := newCachedStore(ctx, remoteURL, "", true)
		if err != nil {
			t.Fatalf("first newCachedStore: %v", err)
		}
		// Wreck the checkout: no .git left.
		if err := os.RemoveAll(filepath.Join(s.dir, ".git")); err != nil {
			t.Fatal(err)
		}

		s, err = newCachedStore(ctx, remoteURL, "", true)
		if err != nil {
			t.Fatalf("newCachedStore after corruption: %v", err)
		}
		if _, err := os.Stat(filepath.Join(s.dir, ".gpg-id")); err != nil {
			t.Errorf("reclone did not restore store content: %v", err)
		}
	})

	t.Run("clones an explicit branch on first use", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, other := setupRemote(t)
		mustGit(t, other, "checkout", "--quiet", "-b", "team-b")
		writeFile(t, other, "team-b-only.gpg", "ciphertext")
		mustGit(t, other, "add", "--all")
		mustGit(t, other, "commit", "--quiet", "-m", "team b secret")
		mustGit(t, other, "push", "--quiet", "origin", "team-b")

		s, err := newCachedStore(ctx, remoteURL, "team-b", true)
		if err != nil {
			t.Fatalf("newCachedStore with branch on first use: %v", err)
		}
		if s.branch != "team-b" {
			t.Errorf("branch = %q; want team-b", s.branch)
		}
		if _, err := os.Stat(filepath.Join(s.dir, "team-b-only.gpg")); err != nil {
			t.Errorf("first clone did not check out the requested branch: %v", err)
		}
	})

	t.Run("switches branch on refresh", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, other := setupRemote(t)
		mustGit(t, other, "checkout", "--quiet", "-b", "team-b")
		writeFile(t, other, "team-b-only.gpg", "ciphertext")
		mustGit(t, other, "add", "--all")
		mustGit(t, other, "commit", "--quiet", "-m", "team b secret")
		mustGit(t, other, "push", "--quiet", "origin", "team-b")

		if _, err := newCachedStore(ctx, remoteURL, "", true); err != nil {
			t.Fatalf("first newCachedStore: %v", err)
		}
		s, err := newCachedStore(ctx, remoteURL, "team-b", true)
		if err != nil {
			t.Fatalf("newCachedStore with branch: %v", err)
		}
		if s.branch != "team-b" {
			t.Errorf("branch = %q; want team-b", s.branch)
		}
		if _, err := os.Stat(filepath.Join(s.dir, "team-b-only.gpg")); err != nil {
			t.Errorf("branch switch did not check out team-b content: %v", err)
		}
	})

	t.Run("missing branch is a configuration error", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, _ := setupRemote(t)

		if _, err := newCachedStore(ctx, remoteURL, "", true); err != nil {
			t.Fatalf("first newCachedStore: %v", err)
		}
		_, err := newCachedStore(ctx, remoteURL, "does-not-exist", true)
		if err == nil {
			t.Fatal("expected an error for a branch missing on the remote")
		}
		if !strings.Contains(err.Error(), "does-not-exist") {
			t.Errorf("error %q does not name the missing branch", err)
		}
	})
}

func TestWithWrite(t *testing.T) {
	ctx := context.Background()

	writeSecret := func(s *gitStore, name, content string) func(context.Context) error {
		return func(context.Context) error {
			path := filepath.Join(s.dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}

			return os.WriteFile(path, []byte(content), 0o600)
		}
	}

	remoteTipMessage := func(t *testing.T, remoteURL string) string {
		t.Helper()

		return mustGit(t, t.TempDir(), "--git-dir", remoteURL, "log", "-1", "--format=%s", "main")
	}

	t.Run("commits with the exact message and pushes", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, _ := setupRemote(t)

		s, err := newCachedStore(ctx, remoteURL, "", true)
		if err != nil {
			t.Fatalf("newCachedStore: %v", err)
		}
		msg := "terraform-provider-pass: write secret/foo"
		if err := s.withWrite(ctx, msg, writeSecret(s, "secret/foo.gpg", "ciphertext")); err != nil {
			t.Fatalf("withWrite: %v", err)
		}

		if got := remoteTipMessage(t, remoteURL); got != msg {
			t.Errorf("remote tip message = %q; want %q", got, msg)
		}
		if out := mustGit(t, s.dir, "status", "--porcelain"); out != "" {
			t.Errorf("worktree not clean after write:\n%s", out)
		}
		if files := mustGit(t, t.TempDir(), "--git-dir", remoteURL, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(files, "secret/foo.gpg") {
			t.Errorf("remote does not contain the written secret, has:\n%s", files)
		}
	})

	t.Run("rebases concurrent remote commits and pushes both", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, other := setupRemote(t)

		s, err := newCachedStore(ctx, remoteURL, "", true)
		if err != nil {
			t.Fatalf("newCachedStore: %v", err)
		}
		// A human pushes a different secret after our configure-time sync.
		pushFromOther(t, other, "human.gpg", "ciphertext", "human write")

		if err := s.withWrite(ctx, "terraform-provider-pass: write ours", writeSecret(s, "ours.gpg", "ciphertext")); err != nil {
			t.Fatalf("withWrite: %v", err)
		}
		files := mustGit(t, t.TempDir(), "--git-dir", remoteURL, "ls-tree", "-r", "--name-only", "main")
		for _, want := range []string{"human.gpg", "ours.gpg"} {
			if !strings.Contains(files, want) {
				t.Errorf("remote lost %s, has:\n%s", want, files)
			}
		}
	})

	t.Run("retries rejected pushes and then succeeds", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, _ := setupRemote(t)

		s, err := newCachedStore(ctx, remoteURL, "", true)
		if err != nil {
			t.Fatalf("newCachedStore: %v", err)
		}
		// A pre-receive hook that rejects the first two pushes.
		hook := "#!/bin/sh\n" +
			"count=$(cat push-count 2>/dev/null || echo 0)\n" +
			"count=$((count + 1))\n" +
			"echo $count > push-count\n" +
			"[ $count -gt 2 ] || { echo rejected by test hook >&2; exit 1; }\n"
		writeHook(t, remoteURL, hook)

		if err := s.withWrite(ctx, "terraform-provider-pass: write retried", writeSecret(s, "retried.gpg", "ciphertext")); err != nil {
			t.Fatalf("withWrite should survive two rejected pushes: %v", err)
		}
		if got := remoteTipMessage(t, remoteURL); got != "terraform-provider-pass: write retried" {
			t.Errorf("remote tip message = %q", got)
		}
	})

	t.Run("gives up after exhausting push retries", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, _ := setupRemote(t)

		s, err := newCachedStore(ctx, remoteURL, "", true)
		if err != nil {
			t.Fatalf("newCachedStore: %v", err)
		}
		writeHook(t, remoteURL, "#!/bin/sh\necho always rejected >&2\nexit 1\n")

		err = s.withWrite(ctx, "terraform-provider-pass: write doomed", writeSecret(s, "doomed.gpg", "ciphertext"))
		if err == nil {
			t.Fatal("expected withWrite to fail when every push is rejected")
		}
		if !strings.Contains(err.Error(), "rejected") {
			t.Errorf("error %q does not explain the rejection", err)
		}
		// The disposable cache must be left pristine, not one commit ahead.
		if out := mustGit(t, s.dir, "status", "--porcelain"); out != "" {
			t.Errorf("cache dirty after failed write:\n%s", out)
		}
		if local, remote := mustGit(t, s.dir, "rev-parse", "HEAD"), mustGit(t, s.dir, "rev-parse", "origin/main"); local != remote {
			t.Error("cache still has the unpushable commit after failure")
		}
	})

	t.Run("same-file conflict fails loudly and resets the cache", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, other := setupRemote(t)

		s, err := newCachedStore(ctx, remoteURL, "", true)
		if err != nil {
			t.Fatalf("newCachedStore: %v", err)
		}
		// Seed a secret both writers will fight over.
		if err := s.withWrite(ctx, "seed contested", writeSecret(s, "contested.gpg", "v1")); err != nil {
			t.Fatalf("seed write: %v", err)
		}

		// The concurrent human write lands while our mutate runs — after
		// withWrite's initial fetch/rebase, before its push.
		mutate := func(mctx context.Context) error {
			pushFromOther(t, other, "contested.gpg", "human version", "human edit of contested")

			return writeSecret(s, "contested.gpg", "terraform version")(mctx)
		}
		err = s.withWrite(ctx, "terraform-provider-pass: write contested", mutate)
		if err == nil {
			t.Fatal("expected a loud failure on a same-file conflict")
		}
		if !strings.Contains(err.Error(), "conflicting concurrent change") {
			t.Errorf("error %q does not describe the conflict", err)
		}

		// Never auto-resolve: the human's version must still be the remote
		// tip, and the cache must be clean on it.
		if got := remoteTipMessage(t, remoteURL); got != "human edit of contested" {
			t.Errorf("remote tip = %q; the provider must not have pushed over the human write", got)
		}
		if out := mustGit(t, s.dir, "status", "--porcelain"); out != "" {
			t.Errorf("cache dirty after conflict:\n%s", out)
		}
	})

	t.Run("no-op mutate pushes nothing", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, _ := setupRemote(t)

		s, err := newCachedStore(ctx, remoteURL, "", true)
		if err != nil {
			t.Fatalf("newCachedStore: %v", err)
		}
		before := mustGit(t, s.dir, "rev-parse", "origin/main")

		if err := s.withWrite(ctx, "should never appear", func(context.Context) error { return nil }); err != nil {
			t.Fatalf("withWrite: %v", err)
		}
		after := mustGit(t, t.TempDir(), "--git-dir", remoteURL, "rev-parse", "main")
		if before != after {
			t.Error("a no-op write moved the remote")
		}
	})

	t.Run("local git store without origin commits but does not push", func(t *testing.T) {
		isolateGitAndCache(t)

		dir := filepath.Join(t.TempDir(), "store")
		mustGit(t, t.TempDir(), "init", "--quiet", "-b", "main", dir)
		writeFile(t, dir, ".gpg-id", "test@example.com\n")
		mustGit(t, dir, "add", "--all")
		mustGit(t, dir, "commit", "--quiet", "-m", "seed")

		s, err := newLocalStore(ctx, dir, false)
		if err != nil {
			t.Fatalf("newLocalStore: %v", err)
		}
		if s.syncing {
			t.Error("a store without origin must not be syncing")
		}
		if err := s.withWrite(ctx, "terraform-provider-pass: write local", writeSecret(s, "local.gpg", "ciphertext")); err != nil {
			t.Fatalf("withWrite: %v", err)
		}
		if got := mustGit(t, dir, "log", "-1", "--format=%s"); got != "terraform-provider-pass: write local" {
			t.Errorf("local commit message = %q", got)
		}
	})

	t.Run("plain directory store writes files without git", func(t *testing.T) {
		isolateGitAndCache(t)

		dir := t.TempDir()
		s, err := newLocalStore(ctx, dir, false)
		if err != nil {
			t.Fatalf("newLocalStore: %v", err)
		}
		if s.isGit || s.syncing {
			t.Errorf("plain dir misdetected: isGit=%v syncing=%v", s.isGit, s.syncing)
		}
		if err := s.withWrite(ctx, "unused", writeSecret(s, "plain.gpg", "ciphertext")); err != nil {
			t.Fatalf("withWrite: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "plain.gpg")); err != nil {
			t.Errorf("secret file missing: %v", err)
		}
	})
}

func TestNewLocalStore(t *testing.T) {
	ctx := context.Background()

	t.Run("detached HEAD fails at configure time with a pointer to repo_url", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, _ := setupRemote(t)

		dir := filepath.Join(t.TempDir(), "modclone")
		mustGit(t, t.TempDir(), "clone", "--quiet", remoteURL, dir)
		mustGit(t, dir, "checkout", "--quiet", "--detach", "HEAD")

		_, err := newLocalStore(ctx, dir, true)
		if err == nil {
			t.Fatal("expected detached HEAD to fail at configure time")
		}
		for _, want := range []string{"detached HEAD", "repo_url"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q should mention %q", err, want)
			}
		}
	})

	t.Run("plain dir inside another git repository stays plain", func(t *testing.T) {
		isolateGitAndCache(t)

		outer := filepath.Join(t.TempDir(), "outer")
		mustGit(t, t.TempDir(), "init", "--quiet", "-b", "main", outer)
		inner := filepath.Join(outer, "store")
		if err := os.MkdirAll(inner, 0o755); err != nil {
			t.Fatal(err)
		}

		s, err := newLocalStore(ctx, inner, false)
		if err != nil {
			t.Fatalf("newLocalStore: %v", err)
		}
		if s.isGit {
			t.Error("a subdirectory of an unrelated repo must not be treated as a git store")
		}
	})

	t.Run("refresh rebases the clone onto origin", func(t *testing.T) {
		isolateGitAndCache(t)
		remoteURL, other := setupRemote(t)

		dir := filepath.Join(t.TempDir(), "clone")
		mustGit(t, t.TempDir(), "clone", "--quiet", remoteURL, dir)
		pushFromOther(t, other, "fresh.gpg", "ciphertext", "fresh secret")

		s, err := newLocalStore(ctx, dir, true)
		if err != nil {
			t.Fatalf("newLocalStore: %v", err)
		}
		if !s.syncing {
			t.Error("clone with origin should be syncing")
		}
		if _, err := os.Stat(filepath.Join(dir, "fresh.gpg")); err != nil {
			t.Errorf("refresh did not pull the new commit: %v", err)
		}
	})

	t.Run("plain dir with refresh on is an error", func(t *testing.T) {
		isolateGitAndCache(t)

		_, err := newLocalStore(ctx, t.TempDir(), true)
		if err == nil {
			t.Fatal("expected refresh_store on a non-git dir to fail")
		}
		if !strings.Contains(err.Error(), "refresh_store") {
			t.Errorf("error %q should mention refresh_store", err)
		}
	})
}

// writeHook installs a pre-receive hook into a bare repository.
func writeHook(t *testing.T, bareRepo, script string) {
	t.Helper()
	hookPath := filepath.Join(bareRepo, "hooks", "pre-receive")
	if err := os.WriteFile(hookPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
