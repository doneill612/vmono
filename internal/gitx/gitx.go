// Package gitx wraps the system git binary. Every vmono git operation goes
// through here so the user's config, credentials, hooks and signing apply,
// and so porcelain output is parsed in exactly one place.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Error is returned when a git invocation exits non-zero. It carries the
// command line and stderr so callers can surface a useful message.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

// Error implements error.
func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

// Unwrap returns the underlying exec error.
func (e *Error) Unwrap() error { return e.Err }

// run executes git with the given arguments in dir (when non-empty) and
// returns trimmed-right stdout. LC_ALL=C keeps parsed output stable and
// GIT_TERMINAL_PROMPT=0 stops background fetches from hanging on a prompt.
func run(dir string, args ...string) (string, error) {
	return runEnv(dir, nil, args...)
}

// runEnv is run with extra KEY=VALUE environment entries appended after the
// defaults, so they win over any duplicate.
func runEnv(dir string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0"), extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return strings.TrimRight(stdout.String(), "\r\n"), nil
}

// Run executes an arbitrary git command in dir and returns its stdout.
func Run(dir string, args ...string) (string, error) { return run(dir, args...) }

// Version returns the installed git version string, e.g. "2.39.5".
func Version() (string, error) {
	out, err := run("", "--version")
	if err != nil {
		return "", err
	}
	f := strings.Fields(out)
	if len(f) < 3 {
		return out, nil
	}
	return f[2], nil
}

// CheckVersion reports an error when git is older than 2.30.
func CheckVersion() error {
	v, err := Version()
	if err != nil {
		return fmt.Errorf("git not found: %w", err)
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return nil
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	if major < 2 || (major == 2 && minor < 30) {
		return fmt.Errorf("git %s is too old; vmono needs 2.30 or newer", v)
	}
	return nil
}

// TopLevel returns the root of the working tree containing path.
func TopLevel(path string) (string, error) {
	out, err := run(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(out), nil
}

// IsBare reports whether the repository at path is bare.
func IsBare(path string) (bool, error) {
	out, err := run(path, "rev-parse", "--is-bare-repository")
	if err != nil {
		return false, err
	}
	return out == "true", nil
}

// CommonDir returns the absolute git common directory for path. For a
// worktree this is the source repo's .git, outside the package.
func CommonDir(path string) (string, error) {
	out, err := run(path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(out), nil
}

// RemoteURL returns the URL of the named remote, or "" when it has none.
func RemoteURL(repo, remote string) string {
	out, err := run(repo, "remote", "get-url", remote)
	if err != nil {
		return ""
	}
	return out
}

// AuthHint is the advice shown when a fetch fails for lack of credentials.
// Fetches never prompt, so the user has to make credentials available
// before retrying.
const AuthHint = "authentication needed: fetches never prompt, so load your key into ssh-agent or sign in through your git credential helper, then retry"

// DefaultFetchConcurrency is how many repos FetchMany fetches at once when
// the caller gives no limit.
const DefaultFetchConcurrency = 4

// authFailureMarkers are lowercase fragments of git and ssh stderr that
// mean a remote refused or could not obtain credentials.
var authFailureMarkers = []string{
	"permission denied (publickey",
	"permission denied, please try again",
	"could not read username",
	"could not read password",
	"authentication failed",
	"terminal prompts disabled",
	"host key verification failed",
}

// IsAuthError reports whether err is a git failure caused by missing or
// rejected credentials, judged from git's stderr. Callers use it to give one
// actionable message in place of repeated raw git output.
func IsAuthError(err error) bool {
	var ge *Error
	if !errors.As(err, &ge) {
		return false
	}
	msg := strings.ToLower(ge.Stderr)
	for _, m := range authFailureMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// nonInteractiveSSH returns the environment that stops ssh from prompting
// during a fetch: BatchMode makes a missing key or unknown host fail at once
// instead of asking on the terminal, which would collide with the TUI and
// with other fetches running in parallel. It returns nil, leaving ssh alone,
// when the user has chosen their own ssh command through GIT_SSH_COMMAND,
// GIT_SSH or core.sshCommand.
func nonInteractiveSSH(repo string) []string {
	if os.Getenv("GIT_SSH_COMMAND") != "" || os.Getenv("GIT_SSH") != "" {
		return nil
	}
	if cfg, err := run(repo, "config", "--get", "core.sshCommand"); err == nil && strings.TrimSpace(cfg) != "" {
		return nil
	}
	return []string{"GIT_SSH_COMMAND=ssh -o BatchMode=yes"}
}

// Fetch runs `git fetch --all --prune` for all remotes. A repo without
// remotes is not an error. It never prompts for credentials: git's terminal
// prompts are off and ssh runs in batch mode, so a fetch that needs
// credentials the user has not made available fails with an error that
// IsAuthError recognizes.
func Fetch(repo string) error {
	out, err := run(repo, "remote")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	_, err = runEnv(repo, nonInteractiveSSH(repo), "fetch", "--all", "--prune")
	return err
}

// FetchResult is the outcome of fetching one distinct repo in FetchMany.
type FetchResult struct {
	// Repo is the first of the requested paths that resolved to this repo.
	Repo string
	// Paths holds every requested path that resolved to this repo,
	// including Repo, in request order.
	Paths []string
	// Err is nil when the fetch succeeded.
	Err error
}

// FetchMany fetches the repos at paths concurrently, running at most limit
// fetches at a time (DefaultFetchConcurrency when limit is below 1). Paths
// that share a git common dir, such as a repo and its worktrees, are one
// repo and are fetched once. It returns one result per distinct repo in the
// order each first appeared in paths, and always fetches every repo: one
// failure never stops the others.
func FetchMany(paths []string, limit int) []FetchResult {
	if limit < 1 {
		limit = DefaultFetchConcurrency
	}
	var results []*FetchResult
	byKey := map[string]*FetchResult{}
	for _, p := range paths {
		key := p
		if cd, err := CommonDir(p); err == nil {
			key = cd
		}
		if r, ok := byKey[key]; ok {
			r.Paths = append(r.Paths, p)
			continue
		}
		r := &FetchResult{Repo: p, Paths: []string{p}}
		byKey[key] = r
		results = append(results, r)
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, limit)
	for _, r := range results {
		wg.Add(1)
		sem <- struct{}{}
		go func(r *FetchResult) {
			defer wg.Done()
			defer func() { <-sem }()
			r.Err = Fetch(r.Repo)
		}(r)
	}
	wg.Wait()

	out := make([]FetchResult, len(results))
	for i, r := range results {
		out[i] = *r
	}
	return out
}

// RefExists reports whether ref resolves to a commit.
func RefExists(repo, ref string) bool {
	_, err := run(repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// ResolveRef returns the commit id for ref.
func ResolveRef(repo, ref string) (string, error) {
	return run(repo, "rev-parse", "--verify", ref+"^{commit}")
}

// ResolveDefaultRef returns the ref to use when a manifest says
// "origin/HEAD". If origin/HEAD does not exist (for example the repo has no
// remote), it falls back to HEAD and reports fellBack=true.
func ResolveDefaultRef(repo, ref string) (resolved string, fellBack bool) {
	if ref == "" {
		ref = "origin/HEAD"
	}
	if RefExists(repo, ref) {
		return ref, false
	}
	if ref == "origin/HEAD" {
		return "HEAD", true
	}
	return ref, false
}

// BranchExists reports whether a local branch exists.
func BranchExists(repo, branch string) bool {
	_, err := run(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// BranchRefConflict reports an existing local branch that stops branch from
// being created. Git stores branches as files under refs/heads, so a branch
// named "vmono/pkg" blocks "vmono/pkg/session" (a file cannot also be a
// directory), and the reverse. An exact match is not a conflict here;
// BranchExists covers that case. The returned name is the branch in the way.
func BranchRefConflict(repo, branch string) (conflict string, found bool) {
	parts := strings.Split(branch, "/")
	for i := 1; i < len(parts); i++ {
		prefix := strings.Join(parts[:i], "/")
		if BranchExists(repo, prefix) {
			return prefix, true
		}
	}
	out, err := run(repo, "for-each-ref", "--count=1", "--format=%(refname:short)", "refs/heads/"+branch+"/")
	if err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out), true
	}
	return "", false
}

// CurrentBranch returns the checked-out branch name, or "" when detached.
func CurrentBranch(repo string) string {
	out, err := run(repo, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// Worktree is one entry of `git worktree list --porcelain`.
type Worktree struct {
	Path     string
	Head     string
	Branch   string // short name; empty when detached
	Detached bool
	Bare     bool
	Prunable bool
}

// WorktreeList returns every worktree registered in repo's common dir.
func WorktreeList(repo string) ([]Worktree, error) {
	out, err := run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil {
			list = append(list, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		if key == "worktree" {
			flush()
			cur = &Worktree{Path: filepath.FromSlash(val)}
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "HEAD":
			cur.Head = val
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "detached":
			cur.Detached = true
		case "bare":
			cur.Bare = true
		case "prunable":
			cur.Prunable = true
		}
	}
	flush()
	return list, nil
}

// BranchCheckedOutAt returns the path of the worktree that has branch
// checked out, or "" when none does. Git allows a branch in only one
// worktree at a time. A worktree whose directory is gone (prunable, for
// example after a package was deleted by hand) holds nothing and is
// ignored, since the next prune would release the branch anyway.
func BranchCheckedOutAt(repo, branch string) (string, error) {
	list, err := WorktreeList(repo)
	if err != nil {
		return "", err
	}
	for _, w := range list {
		if w.Branch == branch && !w.Prunable {
			return w.Path, nil
		}
	}
	return "", nil
}

// WorktreeAddBranch creates a worktree at path on a new branch cut from base.
func WorktreeAddBranch(repo, path, branch, base string) error {
	_, err := run(repo, "worktree", "add", "-b", branch, path, base)
	return err
}

// WorktreeAddExisting checks an existing branch out into a new worktree.
func WorktreeAddExisting(repo, path, branch string) error {
	_, err := run(repo, "worktree", "add", path, branch)
	return err
}

// WorktreeAddDetached creates a detached worktree at ref.
func WorktreeAddDetached(repo, path, ref string) error {
	_, err := run(repo, "worktree", "add", "--detach", path, ref)
	return err
}

// WorktreeRemove removes the worktree at path. Git refuses a dirty worktree
// unless force is set.
func WorktreeRemove(repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := run(repo, args...)
	return err
}

// WorktreePrune drops stale worktree entries from repo.
func WorktreePrune(repo string) error {
	_, err := run(repo, "worktree", "prune")
	return err
}

// WorktreeRepair tells repo that the worktree at path now lives there, after
// its directory was moved on disk. Unlike `git worktree move` it works on
// worktrees containing submodules, and it only rewrites the link files
// between the source repo and the worktree. It needs git 2.29, below vmono's
// minimum.
func WorktreeRepair(repo, path string) error {
	_, err := run(repo, "worktree", "repair", path)
	return err
}

// PruneStale drops repo's stale worktree entries, the ones whose directory
// no longer exists, and returns the paths it released. It does nothing, and
// returns nothing, when every registered worktree is intact. Git leaves
// locked worktrees alone.
func PruneStale(repo string) ([]string, error) {
	list, err := WorktreeList(repo)
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, w := range list {
		if w.Prunable {
			stale = append(stale, w.Path)
		}
	}
	if len(stale) == 0 {
		return nil, nil
	}
	if err := WorktreePrune(repo); err != nil {
		return nil, err
	}
	return stale, nil
}

// Checkout detaches the worktree at path onto ref (used to move reference
// members to their latest fetched ref).
func CheckoutDetached(path, ref string) error {
	_, err := run(path, "checkout", "--detach", ref)
	return err
}

// DeleteBranch deletes a local branch. Without force it uses -d, which git
// refuses when the branch is not merged.
func DeleteBranch(repo, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := run(repo, "branch", flag, branch)
	return err
}

// IsMerged reports whether branch is fully merged into base.
func IsMerged(repo, branch, base string) bool {
	_, err := run(repo, "merge-base", "--is-ancestor", branch, base)
	return err == nil
}

// Status is the parsed state of one worktree.
type Status struct {
	Branch    string // empty when detached
	Head      string // abbreviated commit
	Detached  bool
	Upstream  string
	Ahead     int
	Behind    int
	Dirty     int // tracked files with changes, including conflicts
	Untracked int
}

// StatusOf parses `git status --porcelain=v2 --branch` for the worktree.
func StatusOf(path string) (Status, error) {
	out, err := run(path, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	if err != nil {
		return Status{}, err
	}
	var s Status
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			s.Head = strings.TrimPrefix(line, "# branch.oid ")
			if len(s.Head) > 8 {
				s.Head = s.Head[:8]
			}
		case strings.HasPrefix(line, "# branch.head "):
			h := strings.TrimPrefix(line, "# branch.head ")
			if h == "(detached)" {
				s.Detached = true
			} else {
				s.Branch = h
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			s.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			f := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(f) == 2 {
				s.Ahead, _ = strconv.Atoi(strings.TrimPrefix(f[0], "+"))
				s.Behind, _ = strconv.Atoi(strings.TrimPrefix(f[1], "-"))
			}
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "), strings.HasPrefix(line, "u "):
			s.Dirty++
		case strings.HasPrefix(line, "? "):
			s.Untracked++
		}
	}
	return s, nil
}

// Unpushed counts commits in the worktree that exist on no remote. With an
// upstream it is the commits ahead of it; without one it is commits
// reachable from HEAD but from no remote-tracking ref, so a branch with
// nothing new beyond its base counts zero.
func Unpushed(path string) (int, error) {
	if _, err := run(path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		out, err := run(path, "rev-list", "--count", "@{upstream}..HEAD")
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(out)
	}
	out, err := run(path, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// Init creates a repository at path (creating the directory) on branch.
func Init(path, branch string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	if _, err := run(path, "init", "--quiet", "--initial-branch="+branch); err != nil {
		return err
	}
	return nil
}

// identityArgs supplies a fallback commit identity when the user has none
// configured, so shell-repo commits never fail for lack of one.
func identityArgs(dir string) []string {
	name, _ := run(dir, "config", "user.name")
	email, _ := run(dir, "config", "user.email")
	var args []string
	if strings.TrimSpace(name) == "" {
		args = append(args, "-c", "user.name=vmono")
	}
	if strings.TrimSpace(email) == "" {
		args = append(args, "-c", "user.email=vmono@localhost")
	}
	return args
}

// CommitAll stages everything in dir and commits it with message. It returns
// false when there was nothing to commit. Signing and hooks are not
// bypassed beyond skipping a missing identity.
func CommitAll(dir, message, author string) (bool, error) {
	if _, err := run(dir, "add", "-A"); err != nil {
		return false, err
	}
	if out, err := run(dir, "status", "--porcelain"); err != nil {
		return false, err
	} else if strings.TrimSpace(out) == "" {
		return false, nil
	}
	args := append(identityArgs(dir), "commit", "--quiet", "-m", message)
	if author != "" {
		args = append(args, "--author="+author)
	}
	if _, err := run(dir, args...); err != nil {
		return false, err
	}
	return true, nil
}

// HasCommits reports whether HEAD resolves to a commit.
func HasCommits(dir string) bool {
	_, err := run(dir, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// ErrNotRepo is returned by IsRepo-style checks when a path is not a repo.
var ErrNotRepo = errors.New("not a git repository")
