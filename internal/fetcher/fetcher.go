// Package fetcher handles cloning a GitHub repository using a Personal Access
// Token (PAT).  The PAT is passed to git via a temporary netrc file rather than
// being embedded in the URL, so it never appears as a command-line argument and
// is never written to any log, error message, or output file.
package fetcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// repoNameRe validates that a repo identifier is in the "owner/name" format and
// contains only safe characters, preventing command injection.
var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]+/[A-Za-z0-9_.\-]+$`)

// Fetcher clones remote git repositories to temporary local directories.
type Fetcher struct{}

// New returns a new Fetcher.
func New() *Fetcher { return &Fetcher{} }

// Clone clones the GitHub repository identified by repo (e.g. "owner/name")
// using pat for authentication.  It returns the path to the temporary directory
// containing the clone.  The caller MUST call os.RemoveAll on that directory
// when it is no longer needed.
//
// Security notes:
//   - The PAT is written to a temporary netrc file that is deleted after the
//     clone finishes.  It is never embedded in the clone URL or passed as a
//     command-line argument.
//   - Any error messages are sanitised to remove the PAT before being returned.
//   - repo is validated against a strict allow-list regex before use.
func (f *Fetcher) Clone(pat, repo string) (string, error) {
	if pat == "" {
		return "", fmt.Errorf("pat must not be empty")
	}
	if !repoNameRe.MatchString(repo) {
		return "", fmt.Errorf("invalid repo name %q: must be in the format owner/name using only alphanumeric characters, hyphens, underscores, and dots", repo)
	}

	// Create a temporary home directory so we can write a private netrc file
	// without polluting the real user's home directory.
	tmpHome, err := os.MkdirTemp("", "fog-home-*")
	if err != nil {
		return "", fmt.Errorf("create temp home: %w", err)
	}
	defer os.RemoveAll(tmpHome)

	// Write PAT to a private netrc file (mode 0600).
	if err := writeNetrc(tmpHome, pat); err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "fog-of-war-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	// Build a URL without credentials – credentials are supplied via netrc.
	cloneURL := "https://github.com/" + repo + ".git"

	// GIT_TERMINAL_PROMPT=0 prevents git from blocking on an interactive
	// credentials prompt when the PAT is wrong or missing.
	cmd := exec.Command("git", "clone", "--depth=1", cloneURL, dir) // #nosec G204 – cloneURL constructed from validated repo only
	cmd.Env = safeGitEnv(tmpHome)

	output, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(dir) // best-effort cleanup
		return "", fmt.Errorf("git clone failed: %s", sanitize(string(output), pat))
	}

	return dir, nil
}

// writeNetrc creates a ~/.netrc file that supplies credentials for github.com.
func writeNetrc(home, pat string) error {
	content := "machine github.com login oauth2 password " + pat + "\n"
	path := filepath.Join(home, ".netrc")
	// 0600: readable only by the current user.
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write netrc: %w", err)
	}
	return nil
}

// sanitize replaces all occurrences of secret in s with "***".
func sanitize(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

// safeGitEnv returns a minimal environment for git commands that disables
// interactive prompts and credential helpers to prevent unintended secret
// exposure.
func safeGitEnv(home string) []string {
	return []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=fog-of-war-clearer",
		"GIT_AUTHOR_EMAIL=noreply@fog-of-war-clearer",
		"GIT_COMMITTER_NAME=fog-of-war-clearer",
		"GIT_COMMITTER_EMAIL=noreply@fog-of-war-clearer",
	}
}

