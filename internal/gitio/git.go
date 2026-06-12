package gitio

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("%s", msg)
	}
	return out.String(), nil
}

func InsideWorkTree(ctx context.Context, dir string) bool {
	out, err := Run(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

func WorkTreeRoot(ctx context.Context, dir string) (string, error) {
	out, err := Run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.Clean(strings.TrimSpace(out)), nil
}

func Commit(ctx context.Context, repoDir, message, body string, paths ...string) error {
	args := append([]string{"add", "--"}, paths...)
	if _, err := Run(ctx, repoDir, args...); err != nil {
		return err
	}
	commitArgs := []string{"commit", "-m", message}
	if body != "" {
		commitArgs = append(commitArgs, "-m", body)
	}
	_, err := Run(ctx, repoDir, commitArgs...)
	return err
}

func HasUncommitted(ctx context.Context, repoDir string, paths ...string) (bool, error) {
	args := append([]string{"status", "--porcelain", "--"}, paths...)
	out, err := Run(ctx, repoDir, args...)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}
