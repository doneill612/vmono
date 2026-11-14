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
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
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

// Fetch runs `git fetch --prune` for all remotes. A repo without remotes is
// not an error.
func Fetch(repo string) error {
	out, err := run(repo, "remote")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	_, err = run(repo, "fetch", "--all", "--prune")
	return err
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
// worktree at a time.
func BranchCheckedOutAt(repo, branch string) (string, error) {
	list, err := WorktreeList(repo)
	if err != nil {
		return "", err
	}
	for _, w := range list {
		if w.Branch == branch {
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
