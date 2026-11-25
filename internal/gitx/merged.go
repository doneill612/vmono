package gitx

import (
	"os"
	"path/filepath"
	"strings"
)

// cherry reports, for the commits branch has that base lacks, how many have a
// counterpart on base that makes the same change and how many do not. Merge
// commits are left out, so a base merged into the branch counts for nothing.
func cherry(repo string, env []string, base, branch string) (same, missing int, err error) {
	out, err := runEnv(repo, env, "cherry", base, branch)
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "- "):
			same++
		case strings.HasPrefix(line, "+ "):
			missing++
		}
	}
	return same, missing, nil
}

// RebaseMerged reports whether every commit branch has that base lacks is on
// base as a commit making the same change, which is what a rebase merge or
// cherry-picks leave behind. A branch with no such commits is not.
func RebaseMerged(repo, branch, base string) bool {
	same, missing, err := cherry(repo, nil, base, branch)
	return err == nil && same > 0 && missing == 0
}

// SquashMerged reports whether base has a commit that makes the same change as
// all of branch together, which is what a squash merge leaves behind. It
// squashes the branch onto its merge base as a temporary commit and looks for
// that commit's change on base. The commit is written to a temporary object
// directory, so the repo is only read.
//
// It answers false for a branch with no change of its own, and for one it
// cannot tell about: a squash whose change was edited, or combined with the
// resolution of a conflict, no longer matches.
func SquashMerged(repo, branch, base string) bool {
	fork, err := MergeBase(repo, base, branch)
	if err != nil {
		return false
	}
	if _, err := run(repo, "diff", "--quiet", fork, branch); err == nil {
		return false
	}
	common, err := CommonDir(repo)
	if err != nil {
		return false
	}
	tmp, err := os.MkdirTemp("", "vmono-squash-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(tmp)
	alternates := quoteAlternate(filepath.Join(common, "objects"))
	if set := os.Getenv("GIT_ALTERNATE_OBJECT_DIRECTORIES"); set != "" {
		alternates += string(os.PathListSeparator) + set
	}
	env := []string{"GIT_OBJECT_DIRECTORY=" + tmp, "GIT_ALTERNATE_OBJECT_DIRECTORIES=" + alternates}
	squash, err := runEnv(repo, env, "-c", "user.name=vmono", "-c", "user.email=vmono@localhost",
		"commit-tree", "--no-gpg-sign", "-p", fork, "-m", "squash", branch+"^{tree}")
	if err != nil {
		return false
	}
	same, missing, err := cherry(repo, env, base, squash)
	return err == nil && same == 1 && missing == 0
}

// quoteAlternate spells dir for GIT_ALTERNATE_OBJECT_DIRECTORIES, where a path
// holding the list separator has to be quoted.
func quoteAlternate(dir string) string {
	dir = filepath.ToSlash(dir)
	if !strings.ContainsRune(dir, os.PathListSeparator) && !strings.HasPrefix(dir, `"`) {
		return dir
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(dir) + `"`
}
