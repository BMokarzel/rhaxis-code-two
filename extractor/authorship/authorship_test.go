package authorship_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor/authorship"
)

func gitRun(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Alice",
		"GIT_AUTHOR_EMAIL=alice@example.com",
		"GIT_COMMITTER_NAME=Alice",
		"GIT_COMMITTER_EMAIL=alice@example.com",
	)
	cmd.Env = append(cmd.Env, env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
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

func fileNode(path string) entity.File {
	return entity.File{
		Base: entity.Base{NodeID: "app:test:" + path},
		Path: path,
		Name: filepath.Base(path),
	}
}

// setupRepo cria um repo com 3 commits:
//   - c1 (Alice): adiciona a.ts e b.js
//   - c2 (Bob):   modifica a.ts
//   - c3 (Alice): adiciona c.ts e remove b.js
func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, nil, "init", "-q", "-b", "main")
	gitRun(t, dir, nil, "config", "commit.gpgsign", "false")

	writeFile(t, dir, "a.ts", "export const a = 1;\n")
	writeFile(t, dir, "b.js", "module.exports = 2;\n")
	gitRun(t, dir, nil, "add", ".")
	gitRun(t, dir, nil, "commit", "-q", "-m", "c1")

	writeFile(t, dir, "a.ts", "export const a = 2;\n")
	gitRun(t, dir, []string{
		"GIT_AUTHOR_NAME=Bob", "GIT_AUTHOR_EMAIL=bob@example.com",
		"GIT_COMMITTER_NAME=Bob", "GIT_COMMITTER_EMAIL=bob@example.com",
	}, "add", ".")
	gitRun(t, dir, []string{
		"GIT_AUTHOR_NAME=Bob", "GIT_AUTHOR_EMAIL=bob@example.com",
		"GIT_COMMITTER_NAME=Bob", "GIT_COMMITTER_EMAIL=bob@example.com",
	}, "commit", "-q", "-m", "c2")

	writeFile(t, dir, "c.ts", "export const c = 3;\n")
	if err := os.Remove(filepath.Join(dir, "b.js")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, nil, "add", "-A")
	gitRun(t, dir, nil, "commit", "-q", "-m", "c3")
	return dir
}

func collectEdges(g *entity.Graph, typ entity.EdgeType) []entity.Edge {
	var out []entity.Edge
	for _, e := range g.Edges {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func countNodes(g *entity.Graph, typ entity.NodeType) int {
	n := 0
	for _, node := range g.Nodes {
		if node.Type() == typ {
			n++
		}
	}
	return n
}

func TestEnrichDisabledIsNoop(t *testing.T) {
	g := &entity.Graph{}
	err := authorship.Enrich(context.Background(), g, entity.Application{}, authorship.Input{Repo: "/nowhere"}, authorship.Config{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Error("disabled Enrich mexeu no grafo")
	}
}

func TestEnrichCommitPersonChanged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não disponível")
	}
	dir := setupRepo(t)

	g := &entity.Graph{
		Nodes: []entity.Node{
			fileNode("a.ts"),
			fileNode("c.ts"),
			// b.js intencionalmente ausente — foi deletado no c3, não deveria haver
			// node File para ele; CHANGED(c3, b.js) deve ficar fora do grafo.
		},
	}
	app := entity.Application{Base: entity.Base{NodeID: "app:test"}, Key: "test"}
	cfg := authorship.Config{Enabled: true, StoreEmail: authorship.StoreEmailPlain}
	err := authorship.Enrich(context.Background(), g, app, authorship.Input{Repo: dir, Rev: "HEAD"}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	if n := countNodes(g, entity.CommitNode); n != 3 {
		t.Errorf("Commit nodes: %d, esperado 3", n)
	}
	if n := countNodes(g, entity.PersonNode); n != 2 {
		t.Errorf("Person nodes: %d, esperado 2 (Alice + Bob)", n)
	}

	authored := collectEdges(g, entity.AuthoredByEdge)
	if len(authored) != 3 {
		t.Errorf("AUTHORED_BY edges: %d, esperado 3", len(authored))
	}

	// Committer == Author em todos os 3; COMMITTED_BY não deveria existir
	if n := len(collectEdges(g, entity.CommittedByEdge)); n != 0 {
		t.Errorf("COMMITTED_BY: %d, esperado 0 (committer sempre igual ao author)", n)
	}

	changed := collectEdges(g, entity.ChangedEdge)
	// Esperado: c1 toca a.ts (b.js não existe no grafo); c2 toca a.ts; c3 toca c.ts (b.js deletado não liga)
	// = 3 edges
	if len(changed) != 3 {
		t.Errorf("CHANGED: %d, esperado 3; got %+v", len(changed), changed)
	}
	seenKinds := map[string]int{}
	for _, e := range changed {
		seenKinds[e.Kind]++
	}
	if seenKinds["added"] == 0 {
		t.Error("esperado ao menos um CHANGED com kind=added")
	}
	if seenKinds["modified"] == 0 {
		t.Error("esperado ao menos um CHANGED com kind=modified")
	}
}

func TestEnrichStoreEmailHash(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não disponível")
	}
	dir := setupRepo(t)
	g := &entity.Graph{Nodes: []entity.Node{fileNode("a.ts"), fileNode("c.ts")}}
	cfg := authorship.Config{Enabled: true, StoreEmail: authorship.StoreEmailHash}
	err := authorship.Enrich(context.Background(), g, entity.Application{Base: entity.Base{NodeID: "app:test"}, Key: "test"}, authorship.Input{Repo: dir, Rev: "HEAD"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		p, ok := n.(entity.Person)
		if !ok {
			continue
		}
		if p.Email == "" {
			t.Errorf("Person %s: email vazio apesar de hash", p.Name)
		}
		if !strings.HasPrefix(p.Email, "sha256:") {
			t.Errorf("Person %s: email não hashado: %q", p.Name, p.Email)
		}
		if strings.Contains(p.Email, "@") {
			t.Errorf("Person %s: email contém @ (vazou PII): %q", p.Name, p.Email)
		}
	}
}

func TestEnrichStoreEmailNone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não disponível")
	}
	dir := setupRepo(t)
	g := &entity.Graph{Nodes: []entity.Node{fileNode("a.ts")}}
	cfg := authorship.Config{Enabled: true, StoreEmail: authorship.StoreEmailNone}
	err := authorship.Enrich(context.Background(), g, entity.Application{Base: entity.Base{NodeID: "app:test"}, Key: "test"}, authorship.Input{Repo: dir, Rev: "HEAD"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if p, ok := n.(entity.Person); ok && p.Email != "" {
			t.Errorf("StoreEmailNone: Person %s ainda tem email %q", p.Name, p.Email)
		}
	}
}

func TestLoadFileMissing(t *testing.T) {
	cfg, err := authorship.LoadFile(filepath.Join(t.TempDir(), "nao-existe.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Error("config ausente deveria vir desabilitada")
	}
	if cfg.StoreEmail != authorship.StoreEmailPlain {
		t.Errorf("default StoreEmail: %q", cfg.StoreEmail)
	}
}

func TestLoadFileYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".rhaxis.yaml")
	content := `authorship:
  enabled: true
  store_email: hash
  commits_limit: 50
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := authorship.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Error("enabled deveria estar true")
	}
	if cfg.StoreEmail != authorship.StoreEmailHash {
		t.Errorf("StoreEmail: %q", cfg.StoreEmail)
	}
	if cfg.CommitsLimit != 50 {
		t.Errorf("CommitsLimit: %d", cfg.CommitsLimit)
	}
}

func TestConfigValidateRejectsInvalid(t *testing.T) {
	cfg := authorship.Config{StoreEmail: "cripto"}
	if err := cfg.Validate(); err == nil {
		t.Error("store_email inválido deveria falhar na validação")
	}
}
