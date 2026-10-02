package authorship_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor/authorship"
)

// setupBlameRepo cria um repo com um arquivo TS cuja função cresce por 3
// commits de 2 autores:
//
//	c1 (Alice) — adiciona a função com corpo mínimo (linhas 1..3)
//	c2 (Bob)   — adiciona 2 linhas no corpo (passa a 1..5)
//	c3 (Alice) — adiciona mais 1 linha no corpo (passa a 1..6)
//
// Alice tem autoria de 4 linhas, Bob de 2; Alice fez o primeiro e o último
// commit, portanto CREATED/LAST_MODIFIED → Alice.
func setupBlameRepo(t *testing.T) (dir, finalContent string) {
	t.Helper()
	dir = t.TempDir()
	gitRun(t, dir, nil, "init", "-q", "-b", "main")
	gitRun(t, dir, nil, "config", "commit.gpgsign", "false")

	// c1 — Alice: função de 3 linhas
	writeFile(t, dir, "svc.ts", "export function f() {\n  const x = 1;\n}\n")
	gitRun(t, dir, nil, "add", ".")
	gitRun(t, dir, nil, "commit", "-q", "-m", "c1")

	// c2 — Bob: insere 2 linhas no meio
	writeFile(t, dir, "svc.ts", "export function f() {\n  const x = 1;\n  const y = 2;\n  const z = 3;\n}\n")
	bobEnv := []string{
		"GIT_AUTHOR_NAME=Bob", "GIT_AUTHOR_EMAIL=bob@example.com",
		"GIT_COMMITTER_NAME=Bob", "GIT_COMMITTER_EMAIL=bob@example.com",
	}
	gitRun(t, dir, bobEnv, "add", ".")
	gitRun(t, dir, bobEnv, "commit", "-q", "-m", "c2")

	// c3 — Alice: adiciona mais 1 linha
	finalContent = "export function f() {\n  const x = 1;\n  const y = 2;\n  const z = 3;\n  const w = 4;\n}\n"
	writeFile(t, dir, "svc.ts", finalContent)
	gitRun(t, dir, nil, "add", ".")
	gitRun(t, dir, nil, "commit", "-q", "-m", "c3")
	return dir, finalContent
}

func funcNode(fileID string, startLn, endLn int) entity.Function {
	return entity.Function{
		CodeBase: entity.CodeBase{
			Base:    entity.Base{NodeID: fileID + ":func:f"},
			Loc:     entity.Location{FileID: fileID, StartLine: startLn, StartCol: 1, EndLine: endLn, EndCol: 1},
			OwnerID: fileID,
		},
		Name: "f",
		Kind: entity.FunctionDeclaration,
	}
}

func TestOwnershipBlameEmitsOwnsCreatedLastModified(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não disponível")
	}
	dir, _ := setupBlameRepo(t)

	// Monta grafo: 1 File (svc.ts) + 1 Function cobrindo as 6 linhas.
	fileID := "app:test:svc.ts"
	g := &entity.Graph{
		Nodes: []entity.Node{
			entity.File{Base: entity.Base{NodeID: fileID}, Path: "svc.ts", Name: "svc.ts"},
			funcNode(fileID, 1, 6),
		},
	}
	app := entity.Application{Base: entity.Base{NodeID: "app:test"}, Key: "test"}
	cfg := authorship.Config{Enabled: true, StoreEmail: authorship.StoreEmailPlain}

	err := authorship.Enrich(context.Background(), g, app, authorship.Input{Repo: dir, Rev: "HEAD"}, cfg)
	if err != nil {
		t.Fatal(err)
	}

	owns := collectEdges(g, entity.OwnsEdge)
	if len(owns) == 0 {
		t.Fatal("nenhum OWNS emitido")
	}
	// 2 autores (Alice + Bob) ambos tocaram a função
	seenAuthors := map[string]float64{}
	for _, e := range owns {
		if e.To != fileID+":func:f" {
			t.Errorf("OWNS.To inesperado: %q", e.To)
		}
		if e.Kind != "blame" {
			t.Errorf("OWNS.Kind: %q, esperado 'blame'", e.Kind)
		}
		if e.Share <= 0 || e.Share > 1 {
			t.Errorf("OWNS.Share fora de (0,1]: %v", e.Share)
		}
		seenAuthors[e.From] += e.Share
	}
	if len(seenAuthors) != 2 {
		t.Errorf("esperado 2 autores com OWNS, got %d (%v)", len(seenAuthors), seenAuthors)
	}
	// shares devem somar ~1 (uma linha = um autor)
	var total float64
	for _, s := range seenAuthors {
		total += s
	}
	if total < 0.99 || total > 1.01 {
		t.Errorf("soma dos shares: %v, esperado ~1", total)
	}

	// CREATED e LAST_MODIFIED devem apontar para Alice (primeiro e último commit)
	created := collectEdges(g, entity.CreatedEdge)
	lastMod := collectEdges(g, entity.LastModifiedEdge)
	if len(created) != 1 {
		t.Fatalf("CREATED: %d, esperado 1", len(created))
	}
	if len(lastMod) != 1 {
		t.Fatalf("LAST_MODIFIED: %d, esperado 1", len(lastMod))
	}
	// Alice tem o email alice@example.com — o id da pessoa é "person:" + sha1
	// Em vez de recomputar, procuramos a Person pelo nome.
	personIDByName := map[string]string{}
	for _, n := range g.Nodes {
		if p, ok := n.(entity.Person); ok {
			personIDByName[p.Name] = p.ID()
		}
	}
	if created[0].From != personIDByName["Alice"] {
		t.Errorf("CREATED.From = %q, esperado Alice (%q)", created[0].From, personIDByName["Alice"])
	}
	if lastMod[0].From != personIDByName["Alice"] {
		t.Errorf("LAST_MODIFIED.From = %q, esperado Alice (%q)", lastMod[0].From, personIDByName["Alice"])
	}
}

func TestOwnershipSkipsFilesWithoutDeclarations(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não disponível")
	}
	dir := t.TempDir()
	gitRun(t, dir, nil, "init", "-q", "-b", "main")
	gitRun(t, dir, nil, "config", "commit.gpgsign", "false")
	writeFile(t, dir, "a.ts", "export const a = 1;\n")
	gitRun(t, dir, nil, "add", ".")
	gitRun(t, dir, nil, "commit", "-q", "-m", "c1")

	// File presente mas sem nenhuma declaração associada no grafo.
	fileID := "app:test:a.ts"
	g := &entity.Graph{
		Nodes: []entity.Node{entity.File{Base: entity.Base{NodeID: fileID}, Path: "a.ts", Name: "a.ts"}},
	}
	app := entity.Application{Base: entity.Base{NodeID: "app:test"}, Key: "test"}
	cfg := authorship.Config{Enabled: true, StoreEmail: authorship.StoreEmailPlain}

	err := authorship.Enrich(context.Background(), g, app, authorship.Input{Repo: dir, Rev: "HEAD"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(collectEdges(g, entity.OwnsEdge)); n != 0 {
		t.Errorf("OWNS: %d, esperado 0 (nenhuma declaração)", n)
	}
	if n := len(collectEdges(g, entity.CreatedEdge)); n != 0 {
		t.Errorf("CREATED: %d, esperado 0", n)
	}
}

func TestOwnershipBlameMissingFileIsSoftError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não disponível")
	}
	dir := t.TempDir()
	gitRun(t, dir, nil, "init", "-q", "-b", "main")
	gitRun(t, dir, nil, "config", "commit.gpgsign", "false")
	writeFile(t, dir, "a.ts", "export const a = 1;\n")
	gitRun(t, dir, nil, "add", ".")
	gitRun(t, dir, nil, "commit", "-q", "-m", "c1")

	// File que o grafo diz existir mas que o git não conhece.
	fileID := "app:test:ghost.ts"
	g := &entity.Graph{
		Nodes: []entity.Node{
			entity.File{Base: entity.Base{NodeID: fileID}, Path: "ghost.ts", Name: "ghost.ts"},
			funcNode(fileID, 1, 3),
		},
	}
	app := entity.Application{Base: entity.Base{NodeID: "app:test"}, Key: "test"}
	cfg := authorship.Config{Enabled: true, StoreEmail: authorship.StoreEmailPlain}

	// Redireciona stderr para não poluir o output do teste; o blame falhado
	// escreve "authorship: blame ghost.ts: ..." mas não deve retornar erro.
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if devnull != nil {
		old := os.Stderr
		os.Stderr = devnull
		defer func() { os.Stderr = old; _ = devnull.Close() }()
	}

	err := authorship.Enrich(context.Background(), g, app, authorship.Input{Repo: dir, Rev: "HEAD"}, cfg)
	if err != nil {
		t.Fatalf("blame de arquivo ausente derrubou Enrich: %v", err)
	}
	if n := len(collectEdges(g, entity.OwnsEdge)); n != 0 {
		t.Errorf("OWNS: %d, esperado 0 (file sem blame)", n)
	}
}

// garante que o blame respeita Subdir.
func TestOwnershipWithSubdir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não disponível")
	}
	dir := t.TempDir()
	gitRun(t, dir, nil, "init", "-q", "-b", "main")
	gitRun(t, dir, nil, "config", "commit.gpgsign", "false")
	writeFile(t, dir, filepath.ToSlash("packages/svc/a.ts"), "export function g() {\n  return 1;\n}\n")
	gitRun(t, dir, nil, "add", ".")
	gitRun(t, dir, nil, "commit", "-q", "-m", "c1")

	fileID := "app:test:a.ts" // path RELATIVO ao Subdir, como o git source produz
	g := &entity.Graph{
		Nodes: []entity.Node{
			entity.File{Base: entity.Base{NodeID: fileID}, Path: "a.ts", Name: "a.ts"},
			funcNode(fileID, 1, 3),
		},
	}
	app := entity.Application{Base: entity.Base{NodeID: "app:test"}, Key: "test"}
	cfg := authorship.Config{Enabled: true, StoreEmail: authorship.StoreEmailPlain}

	err := authorship.Enrich(context.Background(), g, app,
		authorship.Input{Repo: dir, Rev: "HEAD", Subdir: "packages/svc"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(collectEdges(g, entity.OwnsEdge)); n == 0 {
		t.Error("esperado OWNS quando Subdir é usado")
	}
}
