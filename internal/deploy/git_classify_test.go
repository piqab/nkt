package deploy

import "testing"

func TestClassifyGitError(t *testing.T) {
	askpass := "git ls-remote: error: unable to read askpass response from '/bin/false'\nfatal: could not read Username for 'https://git.example.com': terminal prompts disabled"
	for _, c := range []struct {
		text     string
		tok, key bool
		want     string
	}{
		{askpass, false, false, GitNoAccess},
		{askpass, true, false, GitTokenRejected},
		{"remote: Invalid username or token. Password authentication is not supported", true, false, GitTokenRejected},
		{"alex@host: Permission denied (publickey).\nfatal: Could not read from remote repository.", false, true, GitKeyRejected},
		{"alex@host: Permission denied (publickey).", false, false, GitNoAccess},
		{"remote: Repository not found.\nfatal: repository 'https://github.com/a/b.git/' not found", true, false, GitNotFound},
		{"fatal: unable to access 'https://nope.invalid/x.git/': Could not resolve host: nope.invalid", false, false, GitHostUnknown},
		{"fatal: something odd", false, false, GitOther},
	} {
		if got := ClassifyGitError(c.text, c.tok, c.key); got != c.want {
			t.Errorf("%q tok=%v key=%v: %s, want %s", c.text, c.tok, c.key, got, c.want)
		}
	}
	if tokenUser("https://gitlab.example.com/a/b.git") != "oauth2" || tokenUser("https://git.example.com/a/b.git") != "x-access-token" {
		t.Error("token user")
	}
}

func TestTokenLogin(t *testing.T) {
	for _, c := range []struct{ repo, tok, user, pass string }{
		{"https://forgejo.example.com/a/b.git", "abc123", "x-access-token", "abc123"},
		{"https://gitlab.com/a/b.git", "glpat-x", "oauth2", "glpat-x"},
		{"https://forgejo.example.com/a/b.git", "alex:abc123", "alex", "abc123"},
	} {
		if u, p := TokenLogin(c.repo, c.tok); u != c.user || p != c.pass {
			t.Errorf("%s %s: %s %s", c.repo, c.tok, u, p)
		}
	}
	if TokenHint("ddb8fd040dccff6fa3534487b84ce32abb97b90c") != "b90c" || TokenHint("short") != "" || TokenHint("alex:ddb8fd040dccff6fa3534487b84ce32abb97b90c") != "b90c" {
		t.Error("hint")
	}
}
