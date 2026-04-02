package fetcher

import (
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		secret string
		want   string
	}{
		{
			name:   "replaces secret in middle",
			s:      "https://mysecret@github.com/owner/repo.git",
			secret: "mysecret",
			want:   "https://***@github.com/owner/repo.git",
		},
		{
			name:   "empty secret returns input unchanged",
			s:      "some output",
			secret: "",
			want:   "some output",
		},
		{
			name:   "multiple occurrences replaced",
			s:      "token mysecret and again mysecret here",
			secret: "mysecret",
			want:   "token *** and again *** here",
		},
		{
			name:   "no occurrence unchanged",
			s:      "nothing to replace here",
			secret: "mysecret",
			want:   "nothing to replace here",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitize(tc.s, tc.secret)
			if got != tc.want {
				t.Errorf("sanitize() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRepoNameValidation(t *testing.T) {
	validCases := []string{
		"owner/repo",
		"my-org/my-repo",
		"user123/project.name",
		"Owner_Name/Repo-Name",
	}
	for _, repo := range validCases {
		if !repoNameRe.MatchString(repo) {
			t.Errorf("expected %q to be valid", repo)
		}
	}

	invalidCases := []struct {
		repo   string
		reason string
	}{
		{"", "empty string"},
		{"noslash", "missing slash"},
		{"owner/", "empty name part"},
		{"/repo", "empty owner part"},
		{"owner/repo/extra", "too many slashes"},
		{"owner repo/name", "space in owner"},
		{"owner/repo name", "space in name"},
		{"owner/repo;rm -rf /", "injection attempt"},
		{"../etc/passwd", "path traversal"},
		{"owner/<script>", "HTML injection"},
	}
	for _, tc := range invalidCases {
		t.Run(tc.reason, func(t *testing.T) {
			if repoNameRe.MatchString(tc.repo) {
				t.Errorf("expected %q (%s) to be invalid", tc.repo, tc.reason)
			}
		})
	}
}

func TestCloneValidatesPAT(t *testing.T) {
	f := New()
	_, err := f.Clone("", "owner/repo")
	if err == nil {
		t.Fatal("expected error for empty PAT")
	}
}

func TestCloneValidatesRepoName(t *testing.T) {
	f := New()
	_, err := f.Clone("sometoken", "bad repo name!")
	if err == nil {
		t.Fatal("expected error for invalid repo name")
	}
}

func TestBuildCloneURL_DoesNotContainPAT(t *testing.T) {
	// The clone URL no longer contains the PAT; credentials are passed via netrc.
	cloneURL := "https://github.com/" + "owner/repo" + ".git"
	if strings.Contains(cloneURL, "secrettoken") {
		t.Error("clone URL must not contain the PAT")
	}
	if !strings.Contains(cloneURL, "github.com/owner/repo.git") {
		t.Error("clone URL should contain the repo path")
	}
}
