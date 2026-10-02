package git_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BMokarzel/rhaxis-code-two/extractor/source"
	gitsrc "github.com/BMokarzel/rhaxis-code-two/extractor/source/git"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source/local"
)

// langs replica o map de extensões usado em testes JS/TS.
var langs = map[string]string{
	".js":  "javascript",
	".ts":  "typescript",
	".tsx": "tsx",
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Rhaxis Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Rhaxis Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initRepoWithTwoCommits(t *testing.T) (dir, sha1, sha2 string) {
	t.Helper()
	dir = t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "commit.gpgsign", "false")

	writeFile(t, dir, "a.ts", "export const greet = () => 'hi';\n")
	writeFile(t, dir, "sub/b.js", "function b() { return 1; }\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	sha1 = gitOutput(t, dir, "rev-parse", "HEAD")

	writeFile(t, dir, "a.ts", "export const greet = () => 'ola';\n")
	writeFile(t, dir, "sub/c.ts", "export const c = 2;\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "second")
	sha2 = gitOutput(t, dir, "rev-parse", "HEAD")
	return
}

func collect(t *testing.T, p source.Provider) map[string]source.File {
	t.Helper()
	out := map[string]source.File{}
	err := p.Walk(context.Background(), func(f source.File) error {
		out[f.Path] = f
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

func TestGitSourceSelectsRevision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não disponível")
	}
	dir, sha1, sha2 := initRepoWithTwoCommits(t)

	first := collect(t, gitsrc.New(dir, sha1, "", langs))
	second := collect(t, gitsrc.New(dir, sha2, "", langs))

	if _, ok := first["sub/c.ts"]; ok {
		t.Error("commit 1 não deveria ter sub/c.ts")
	}
	if _, ok := second["sub/c.ts"]; !ok {
		t.Error("commit 2 deveria ter sub/c.ts")
	}
	if got := string(first["a.ts"].Content); !strings.Contains(got, "hi") {
		t.Errorf("commit 1 a.ts: %q", got)
	}
	if got := string(second["a.ts"].Content); !strings.Contains(got, "ola") {
		t.Errorf("commit 2 a.ts: %q", got)
	}

	// Hash deve ser o SHA do blob (40 chars hex)
	for _, f := range first {
		if len(f.Hash) != 40 {
			t.Errorf("hash não parece SHA do blob: %q", f.Hash)
		}
	}
}

func TestGitSourceSubdir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não disponível")
	}
	dir, _, sha2 := initRepoWithTwoCommits(t)

	only := collect(t, gitsrc.New(dir, sha2, "sub", langs))

	paths := make([]string, 0, len(only))
	for p := range only {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	want := []string{"b.js", "c.ts"}
	if !equal(paths, want) {
		t.Errorf("paths com subdir=sub: %v, esperado %v", paths, want)
	}
}

func TestGitSourceEquivalenceWithLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não disponível")
	}
	dir, _, _ := initRepoWithTwoCommits(t)

	fromGit := collect(t, gitsrc.New(dir, "HEAD", "", langs))
	fromLocal := collect(t, local.New(dir, langs))
	// source/local vai enxergar .git/... só se as extensões casarem; não casa.
	// Normaliza chaves.
	gitPaths := keys(fromGit)
	localPaths := keys(fromLocal)
	sort.Strings(gitPaths)
	sort.Strings(localPaths)
	if !equal(gitPaths, localPaths) {
		t.Fatalf("conjuntos diferentes:\n git=%v\n local=%v", gitPaths, localPaths)
	}
	for p, gf := range fromGit {
		lf := fromLocal[p]
		if gf.Language != lf.Language {
			t.Errorf("%s: language git=%q local=%q", p, gf.Language, lf.Language)
		}
		if !bytes.Equal(gf.Content, lf.Content) {
			t.Errorf("%s: content differ", p)
		}
	}
}

func TestGitSourceReadFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não disponível")
	}
	dir, _, sha2 := initRepoWithTwoCommits(t)
	writeFile(t, dir, "package.json", `{"name":"x"}`)
	gitRun(t, dir, "add", "package.json")
	gitRun(t, dir, "commit", "-q", "-m", "add pkg")
	head := gitOutput(t, dir, "rev-parse", "HEAD")

	p := gitsrc.New(dir, head, "", langs)
	content, err := p.ReadFile(context.Background(), "package.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != `{"name":"x"}` {
		t.Errorf("ReadFile package.json: %q", string(content))
	}

	// Em revisões antigas, o arquivo não existe
	old := gitsrc.New(dir, sha2, "", langs)
	if _, err := old.ReadFile(context.Background(), "package.json"); err == nil {
		t.Error("ReadFile em revisão sem o arquivo deveria falhar")
	}
}

func keys(m map[string]source.File) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
