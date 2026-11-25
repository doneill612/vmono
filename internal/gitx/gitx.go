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
	"sync/atomic"
	"time"
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

// versionOK records that CheckVersion has passed in this process.
var versionOK atomic.Bool

// CheckVersion reports an error when git is older than 2.30. Once it has
// passed it doesn't ask git again; a failure is never remembered, so the user
// can fix it while vmono runs.
func CheckVersion() error {
	if versionOK.Load() {
		return nil
	}
	err := checkVersion()
	versionOK.Store(err == nil)
	return err
}

// checkVersion asks git for its version and compares it.
func checkVersion() error {
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

// WorkTree returns the root of the working tree containing path, as TopLevel
// does, and whether its repository is bare, with one git command.
func WorkTree(path string) (top string, bare bool, err error) {
	out, err := run(path, "rev-parse", "--show-toplevel", "--is-bare-repository")
	if err != nil {
		return "", false, err
	}
	i := strings.LastIndexByte(out, '\n')
	if i < 0 {
		return "", false, fmt.Errorf("unexpected output from git rev-parse: %q", out)
	}
	return filepath.FromSlash(strings.TrimRight(out[:i], "\r\n")), out[i+1:] == "true", nil
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

// HasAnyCommit reports whether repo holds any commit at all, on any branch,
// unlike HasCommits, which asks only about HEAD. A freshly `git init`ed repo
// holds none: its HEAD names a branch that is not born yet, so no ref in it
// resolves to a commit and nothing can be branched from.
func HasAnyCommit(repo string) bool {
	out, err := run(repo, "rev-list", "-n", "1", "--all")
	return err == nil && out != ""
}

// ResolveRef returns the commit id for ref.
func ResolveRef(repo, ref string) (string, error) {
	return run(repo, "rev-parse", "--verify", ref+"^{commit}")
}

// ResolveDefaultRef returns the ref to use when a manifest says
// "origin/HEAD". If origin/HEAD does not exist (for example the repo has no
// remote, or a clone whose origin/HEAD was never set), it falls back to what
// the repo has checked out and reports fellBack=true.
//
// The fallback is never the literal "HEAD". A ref returned here is also
// evaluated inside session worktrees, where "HEAD" is the session branch
// itself: a base of "HEAD" there makes every one of the session's own commits
// look like they are already on the base, so status and finish see nothing to
// land. The fallback is therefore pinned to the source's checked-out branch
// as a full ref name (refs/heads/<branch>, which every worktree of the repo
// resolves the same way), or to a commit id when the source has a detached
// HEAD or the checkout cannot be named.
func ResolveDefaultRef(repo, ref string) (resolved string, fellBack bool) {
	return LiveRefs(repo).ResolveDefault(ref)
}

// ResolveDefault is ResolveDefaultRef answered from r.
func (r Refs) ResolveDefault(ref string) (resolved string, fellBack bool) {
	repo := r.repo
	if ref == "" {
		ref = "origin/HEAD"
	}
	if r.Exists(ref) {
		return ref, false
	}
	if ref != "origin/HEAD" {
		return ref, false
	}
	if out, err := run(repo, "symbolic-ref", "--quiet", "HEAD"); err == nil && out != "" && RefExists(repo, out) {
		return out, true
	}
	if id, err := ResolveRef(repo, "HEAD"); err == nil {
		return id, true
	}
	return "HEAD", true
}

// BranchExists reports whether a local branch exists.
func BranchExists(repo, branch string) bool {
	_, err := run(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// ValidBranchName reports whether name is a valid local branch name under
// git's ref-format rules, run from repo so `@{-1}` style shorthands are not
// mistaken for plain names.
func ValidBranchName(repo, name string) bool {
	out, err := run(repo, "check-ref-format", "--branch", name)
	return err == nil && out == name
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
// The branch tracks nothing, whatever branch.autoSetupMerge says: git would
// otherwise make a branch cut from origin/main track it, and a push that
// follows the upstream would then publish the branch's commits to main.
func WorktreeAddBranch(repo, path, branch, base string) error {
	_, err := run(repo, "worktree", "add", "--no-track", "-b", branch, path, base)
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

// MergeBase returns the best common ancestor commit of a and b. It fails
// when the two share no history.
func MergeBase(repo, a, b string) (string, error) {
	return run(repo, "merge-base", a, b)
}

// RevCount counts the commits selected by a `git rev-list` revision
// expression, for example RevCount(repo, "main..feature") or
// RevCount(repo, "HEAD", "--not", "main", "origin/main").
func RevCount(repo string, revs ...string) (int, error) {
	out, err := run(repo, append([]string{"rev-list", "--count"}, revs...)...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// AheadBehind returns how many commits a has that b lacks, and how many b has
// that a lacks.
func AheadBehind(repo, a, b string) (ahead, behind int, err error) {
	out, err := run(repo, "rev-list", "--count", "--left-right", a+"..."+b)
	if err != nil {
		return 0, 0, err
	}
	if _, err := fmt.Sscan(out, &ahead, &behind); err != nil {
		return 0, 0, fmt.Errorf("unexpected output from git rev-list: %q", out)
	}
	return ahead, behind, nil
}

// RebaseOnto replays the commits of the worktree's checked-out branch that
// are reachable from HEAD but not from upstream onto onto, equivalent to
// `git rebase --onto <onto> <upstream>`. Git stops with an error on a
// conflict and leaves the rebase in progress in the worktree, so the caller
// can report ConflictedFiles and let the user or agent resolve them and run
// `git rebase --continue`. A missing commit identity is filled in the same
// way CommitAll fills it.
func RebaseOnto(worktree, onto, upstream string) error {
	args := append(identityArgs(worktree), "rebase", "--onto", onto, upstream)
	_, err := run(worktree, args...)
	return err
}

// RebaseAbort gives up a rebase in progress in the worktree, putting its branch
// and files back as they were before it started.
func RebaseAbort(worktree string) error {
	_, err := run(worktree, "rebase", "--abort")
	return err
}

// Merge merges ref into the worktree's checked-out branch, without opening an
// editor. On a conflict git stops and leaves the merge in progress. A missing
// commit identity is filled in the same way CommitAll fills it.
func Merge(worktree, ref string) error {
	_, err := run(worktree, append(identityArgs(worktree), "merge", "--no-edit", "--quiet", ref)...)
	return err
}

// MergeAbort gives up a merge in progress in the worktree, putting its branch
// and files back as they were before it started.
func MergeAbort(worktree string) error {
	_, err := run(worktree, "merge", "--abort")
	return err
}

// RebaseInProgress reports whether the worktree is in the middle of a
// rebase, stopped on a conflict or waiting on `git rebase --continue`.
func RebaseInProgress(worktree string) bool {
	out, err := run(worktree, "rev-parse", "--path-format=absolute", "--git-path", "rebase-merge", "--git-path", "rebase-apply")
	if err != nil {
		return false
	}
	for _, dir := range strings.Split(out, "\n") {
		if st, err := os.Stat(filepath.FromSlash(strings.TrimRight(dir, "\r"))); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

// ConflictedFiles lists the paths with unresolved merge conflicts in the
// worktree, relative to its root.
func ConflictedFiles(worktree string) ([]string, error) {
	out, err := run(worktree, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, filepath.FromSlash(line))
		}
	}
	return files, nil
}

// ResetKeep moves the worktree's checked-out branch to ref and updates its
// working tree, refusing when that would overwrite local changes: git keeps
// uncommitted changes that do not conflict and aborts when any would be lost,
// so it never discards work.
func ResetKeep(worktree, ref string) error {
	_, err := run(worktree, "reset", "--keep", "--quiet", ref)
	return err
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

// OnNoBranch counts the commits of the worktree's HEAD that no branch, tag or
// remote branch holds. It is for a detached HEAD, as reference members are
// checked out and a rebase leaves: removing the worktree loses only these.
func OnNoBranch(path string) (int, error) {
	return RevCount(path, "HEAD", "--not", "--branches", "--tags", "--remotes")
}

// Tracking is the remote branch a local branch is configured to track.
type Tracking struct {
	// Remote is the name of the remote, such as "origin".
	Remote string
	// Branch is the branch's name on that remote, which need not match the
	// local branch's name.
	Branch string
	// Ref is the remote-tracking ref that mirrors it locally, such as
	// "refs/remotes/origin/main". It may not exist, for example when the
	// remote branch was deleted and pruned.
	Ref string
}

// UpstreamOf returns the remote branch the local branch tracks. The second
// result is false when the branch has no upstream, or tracks another local
// branch rather than a remote one.
func UpstreamOf(repo, branch string) (Tracking, bool) {
	out, err := run(repo, "for-each-ref", "--format=%(upstream)\t%(upstream:remotename)", "refs/heads/"+branch)
	if err != nil {
		return Tracking{}, false
	}
	ref, remote, _ := strings.Cut(strings.TrimSpace(out), "\t")
	return newTracking(tracked{ref: ref, remote: strings.TrimSpace(remote)})
}

// RemoteBranch is a branch of a remote as of the last fetch.
type RemoteBranch struct {
	// Name is the branch's name on the remote, without the remote's.
	Name string
	// When is the time of its latest commit.
	When time.Time
}

// RemoteBranches lists the branches of remote as of the last fetch, the one
// with the most recent commit first.
func RemoteBranches(repo, remote string) ([]RemoteBranch, error) {
	prefix := "refs/remotes/" + remote + "/"
	out, err := run(repo, "for-each-ref", "--sort=-committerdate", "--format=%(committerdate:unix) %(refname)", prefix)
	if err != nil {
		return nil, err
	}
	var list []RemoteBranch
	for _, line := range strings.Split(out, "\n") {
		when, ref, _ := strings.Cut(strings.TrimSpace(line), " ")
		name := strings.TrimPrefix(ref, prefix)
		if name == "" || name == "HEAD" {
			continue
		}
		sec, _ := strconv.ParseInt(when, 10, 64)
		list = append(list, RemoteBranch{Name: name, When: time.Unix(sec, 0)})
	}
	return list, nil
}

// SetUpstream makes the local branch track upstream, such as "origin/main".
func SetUpstream(repo, branch, upstream string) error {
	_, err := run(repo, "branch", "--set-upstream-to="+upstream, branch)
	return err
}

// UnsetUpstream makes the local branch track nothing. It fails when the branch
// has no upstream.
func UnsetUpstream(repo, branch string) error {
	_, err := run(repo, "branch", "--unset-upstream", branch)
	return err
}

// RefMove is one entry of a ref's reflog.
type RefMove struct {
	// Commit is where the ref pointed afterwards.
	Commit string
	// Reason is what moved it, in git's words, such as "update by push".
	Reason string
}

// Pushed reports whether the move was a push from this repo.
func (m RefMove) Pushed() bool { return m.Reason == "update by push" }

// Reflog returns where ref has pointed, the most recent first. It is nil for a
// ref with no reflog.
func Reflog(repo, ref string) []RefMove {
	out, err := run(repo, "reflog", "show", "--format=%H %gs", ref, "--")
	if err != nil || out == "" {
		return nil
	}
	var moves []RefMove
	for _, line := range strings.Split(out, "\n") {
		commit, reason, _ := strings.Cut(strings.TrimRight(line, "\r"), " ")
		moves = append(moves, RefMove{Commit: commit, Reason: reason})
	}
	return moves
}

// Remotes returns the names of the repo's remotes, in git's order.
func Remotes(repo string) []string {
	out, err := run(repo, "remote")
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// Push publishes the local branch to the named branch on the remote, as
// `git push [-u] <remote> refs/heads/<branch>:refs/heads/<dest>`. With
// setUpstream the local branch is made to track it. It never forces, so a
// remote branch that has commits the local one lacks is refused, and it never
// prompts for credentials: git's terminal prompts are off and ssh runs in
// batch mode, so a push that needs credentials the user has not made available
// fails with an error IsAuthError recognizes. Nothing but the one branch is
// sent, whatever push.default or the remote's push refspecs say.
func Push(repo, remote, branch, dest string, setUpstream bool) error {
	args := []string{"push"}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	args = append(args, remote, "refs/heads/"+branch+":refs/heads/"+dest)
	_, err := runEnv(repo, nonInteractiveSSH(repo), args...)
	return err
}

// nonFastForwardMarkers are lowercase fragments of git stderr that mean a push
// was refused because the remote branch has commits the local one lacks.
var nonFastForwardMarkers = []string{
	"non-fast-forward",
	"fetch first",
	"tip of your current branch is behind",
}

// IsNonFastForward reports whether err is a push the remote refused because
// its branch is ahead of, or has diverged from, the one pushed, judged from
// git's stderr. A refusal by a hook or a protected branch is not one.
func IsNonFastForward(err error) bool {
	var ge *Error
	if !errors.As(err, &ge) {
		return false
	}
	msg := strings.ToLower(ge.Stderr)
	for _, m := range nonFastForwardMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// DefaultBranch returns the name of the branch a repo's work normally lands
// on: the one origin/HEAD points at, or, for a repo with no remote HEAD, the
// branch it has checked out. It returns "" when neither is known.
func DefaultBranch(repo string) string {
	if out, err := run(repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if _, branch, ok := strings.Cut(out, "/"); ok && branch != "" {
			return branch
		}
	}
	return CurrentBranch(repo)
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
	out, _ := run(dir, "config", "--get-regexp", `^user\.(name|email)$`)
	var name, email string
	for _, line := range strings.Split(out, "\n") {
		// The last value of a key wins, as it does for git.
		switch key, value, _ := strings.Cut(strings.TrimRight(line, "\r"), " "); key {
		case "user.name":
			name = value
		case "user.email":
			email = value
		}
	}
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
