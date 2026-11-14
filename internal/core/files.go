package core

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// copyGlobs copies untracked files matching globs from a source repo into a
// new worktree and returns how many files were copied. Worktrees hold only
// tracked files, so .env files and local skills would not otherwise come
// along.
//
// Patterns are relative to the repo and use filepath.Glob syntax. Absolute
// patterns and ".." are rejected. Symlinks are skipped, never followed or
// recreated. Anything inside .git is skipped, and a destination file that
// already exists (a tracked file) is never overwritten.
func copyGlobs(srcRoot, dstRoot string, globs []string) (int, error) {
	copied := 0
	for _, g := range globs {
		clean := filepath.ToSlash(filepath.Clean(g))
		if filepath.IsAbs(g) || strings.HasPrefix(g, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			return copied, fmt.Errorf("copy pattern %q must be relative and stay inside the repo", g)
		}
		matches, err := filepath.Glob(filepath.Join(srcRoot, filepath.FromSlash(clean)))
		if err != nil {
			return copied, fmt.Errorf("copy pattern %q: %w", g, err)
		}
		for _, m := range matches {
			n, err := copyTree(srcRoot, dstRoot, m)
			copied += n
			if err != nil {
				return copied, err
			}
		}
	}
	return copied, nil
}

// copyTree copies one matched file or directory tree.
func copyTree(srcRoot, dstRoot, match string) (int, error) {
	copied := 0
	err := filepath.WalkDir(match, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, p)
		if err != nil {
			return err
		}
		if rel == ".git" || strings.HasPrefix(filepath.ToSlash(rel), ".git/") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		dst := filepath.Join(dstRoot, rel)
		if _, err := os.Lstat(dst); err == nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := copyFile(p, dst, info.Mode().Perm()); err != nil {
			return err
		}
		copied++
		return nil
	})
	return copied, err
}

// copyFile copies a regular file, preserving permission bits.
func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
