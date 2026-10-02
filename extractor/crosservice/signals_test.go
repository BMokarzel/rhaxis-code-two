package crosservice_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor/crosservice"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source/local"
)

func appNode(key string) entity.Application {
	return entity.Application{Base: entity.Base{NodeID: "app:" + key}, Name: key, Key: key}
}

func findApp(g *entity.Graph, id string) *entity.Application {
	for _, n := range g.Nodes {
		if a, ok := n.(entity.Application); ok && a.ID() == id {
			return &a
		}
	}
	return nil
}

func TestSignalsCollectsEndpoints(t *testing.T) {
	app := appNode("users")
	g := &entity.Graph{Nodes: []entity.Node{
		app,
		entity.Endpoint{Base: entity.Base{NodeID: "ep1"}, Method: "GET", Path: "/users/:id"},
		entity.Endpoint{Base: entity.Base{NodeID: "ep2"}, Method: "POST", Path: "/users"},
		// duplicata exata deve ser filtrada
		entity.Endpoint{Base: entity.Base{NodeID: "ep3"}, Method: "GET", Path: "/users/:id"},
	}}

	e := crosservice.SignalsEnricher{} // sem Src
	if err := e.Enrich(context.Background(), app, g); err != nil {
		t.Fatal(err)
	}
	out := findApp(g, app.ID())
	if out == nil {
		t.Fatal("Application sumiu do grafo")
	}
	if len(out.Endpoints) != 2 {
		t.Errorf("Endpoints = %v, esperado 2", out.Endpoints)
	}
	// ordenação determinística
	if out.Endpoints[0] != "GET /users/:id" {
		t.Errorf("Endpoints[0] = %q", out.Endpoints[0])
	}
}

func TestSignalsCollectsEnvVars(t *testing.T) {
	app := appNode("svc")
	proc := entity.External{Base: entity.Base{NodeID: "app:svc/ext:process"}, Name: "process"}
	otherExt := entity.External{Base: entity.Base{NodeID: "app:svc/ext:console"}, Name: "console"}
	fn := entity.Function{CodeBase: entity.CodeBase{Base: entity.Base{NodeID: "app:svc:f.ts:fn"}}}
	g := &entity.Graph{
		Nodes: []entity.Node{app, proc, otherExt, fn},
		Edges: []entity.Edge{
			{Type: entity.ReadsEdge, From: fn.ID(), To: proc.ID(), Member: "env.DB_URL"},
			{Type: entity.ReadsEdge, From: fn.ID(), To: proc.ID(), Member: "env.API_KEY.length"}, // só primeiro componente
			{Type: entity.ReadsEdge, From: fn.ID(), To: proc.ID(), Member: "env.DB_URL"},        // duplicata
			{Type: entity.ReadsEdge, From: fn.ID(), To: proc.ID(), Member: "pid"},               // não é env.
			{Type: entity.ReadsEdge, From: fn.ID(), To: otherExt.ID(), Member: "env.LEAK"},       // não é process
		},
	}

	e := crosservice.SignalsEnricher{}
	if err := e.Enrich(context.Background(), app, g); err != nil {
		t.Fatal(err)
	}
	out := findApp(g, app.ID())
	if out == nil {
		t.Fatal("Application sumiu")
	}
	if len(out.EnvVarsRead) != 2 {
		t.Errorf("EnvVarsRead = %v, esperado 2", out.EnvVarsRead)
	}
	// ordenação alfabética
	if out.EnvVarsRead[0] != "API_KEY" || out.EnvVarsRead[1] != "DB_URL" {
		t.Errorf("ordem errada: %v", out.EnvVarsRead)
	}
}

func TestSignalsNoSourceSkipsHosts(t *testing.T) {
	app := appNode("x")
	g := &entity.Graph{Nodes: []entity.Node{app}}
	e := crosservice.SignalsEnricher{}
	if err := e.Enrich(context.Background(), app, g); err != nil {
		t.Fatal(err)
	}
	out := findApp(g, app.ID())
	if len(out.HostCandidates) != 0 {
		t.Errorf("HostCandidates deveria estar vazio sem Src: %v", out.HostCandidates)
	}
}

func TestSignalsCollectsHostCandidatesFromSource(t *testing.T) {
	dir := t.TempDir()
	code := "const base = 'http://users:8080/users';\n" +
		"const api = 'https://api.internal/v1';\n" +
		"fetch('http://users:8080/users/1');\n" + // host duplicado, dedup
		"// just a comment, not a url: foo:bar\n"
	if err := os.WriteFile(filepath.Join(dir, "a.ts"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	src := local.New(dir, map[string]string{".ts": "typescript"})
	app := appNode("svc")
	g := &entity.Graph{Nodes: []entity.Node{app}}

	e := crosservice.SignalsEnricher{Src: src}
	if err := e.Enrich(context.Background(), app, g); err != nil {
		t.Fatal(err)
	}
	out := findApp(g, app.ID())
	if len(out.HostCandidates) != 2 {
		t.Errorf("HostCandidates = %v, esperado 2", out.HostCandidates)
	}
	seen := map[string]bool{}
	for _, h := range out.HostCandidates {
		seen[h] = true
	}
	if !seen["users:8080"] || !seen["api.internal"] {
		t.Errorf("hosts esperados ausentes: %v", out.HostCandidates)
	}
}

func TestSignalsAttachesAppIfMissing(t *testing.T) {
	app := appNode("y")
	g := &entity.Graph{} // Application fora do grafo
	e := crosservice.SignalsEnricher{}
	if err := e.Enrich(context.Background(), app, g); err != nil {
		t.Fatal(err)
	}
	if findApp(g, app.ID()) == nil {
		t.Error("Application não foi adicionada ao grafo")
	}
}
