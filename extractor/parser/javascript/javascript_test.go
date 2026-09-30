package javascript_test

import (
	"context"
	"testing"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor"
	"github.com/BMokarzel/rhaxis-code-two/extractor/linker"
	jsresolver "github.com/BMokarzel/rhaxis-code-two/extractor/linker/javascript"
	"github.com/BMokarzel/rhaxis-code-two/extractor/parser"
	"github.com/BMokarzel/rhaxis-code-two/extractor/parser/javascript"
	"github.com/BMokarzel/rhaxis-code-two/extractor/repository/memory"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source/local"
)

const (
	ctrl   = "nest:src/users/users.controller.ts#"
	svc    = "nest:src/users/users.service.ts#"
	repo   = "nest:src/users/users.repository.ts#"
	dto    = "nest:src/users/dto/create-user.dto.ts#"
	logger = "nest:src/shared/logger.ts#"
	legacy = "nest:src/legacy.js#"
)

func extractSample(t *testing.T) *entity.Graph {
	t.Helper()
	registry := parser.NewRegistry(javascript.New())
	js := jsresolver.NewFactory()
	mem := memory.New()
	o := &extractor.Extractor{
		Parser: registry,
		Linker: linker.New(map[string]linker.ResolverFactory{
			javascript.LangJavaScript: js, javascript.LangTypeScript: js, javascript.LangTSX: js,
		}),
		Repository: mem,
	}
	app := entity.Application{Base: entity.Base{NodeID: "app:nest"}, Name: "nest", Key: "nest"}
	report, err := o.Run(context.Background(), app, local.New("../../../testdata/nest-sample", registry.Extensions()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range report.Failed {
		t.Errorf("falha em %s: %v", f.Path, f.Err)
	}
	return mem.Graph(app.ID())
}

func hasEdge(g *entity.Graph, typ entity.EdgeType, from, to string) *entity.Edge {
	for i, e := range g.Edges {
		if e.Type == typ && e.From == from && e.To == to {
			return &g.Edges[i]
		}
	}
	return nil
}

func node(g *entity.Graph, id string) entity.Node {
	for _, n := range g.Nodes {
		if n.ID() == id {
			return n
		}
	}
	return nil
}

func TestNestSample(t *testing.T) {
	g := extractSample(t)

	edges := []struct {
		typ      entity.EdgeType
		from, to string
	}{
		// controller → service, atravessando o barrel file e o parameter property do construtor
		{entity.HasTypeEdge, ctrl + "UsersController.usersService", svc + "UsersService"},
		{entity.InvokesEdge, ctrl + "UsersController.getUser", svc + "UsersService.findOne"},
		{entity.InvokesEdge, ctrl + "UsersController.create", svc + "UsersService.create"},
		// export { CreateUserDto as UserInput } from ...
		{entity.HasTypeEdge, ctrl + "UsersController.create(body)", dto + "CreateUserDto"},
		// service → repository e logger (alias @shared do tsconfig, tipo inferido por new)
		{entity.InvokesEdge, svc + "UsersService.findOne", repo + "UsersRepository.findById"},
		{entity.InvokesEdge, svc + "UsersService.create", repo + "UsersRepository.save"},
		{entity.InvokesEdge, svc + "UsersService.findOne", logger + "AppLogger.log"},
		{entity.ReadsEdge, svc + "UsersService.create", repo + "UserStatus.Active"},
		{entity.HasFieldEdge, dto + "CreateUserDto", dto + "CreateUserDto.email"},
		{entity.ImportsEdge, "nest:src/users/users.controller.ts", "nest:src/users/index.ts"},
		{entity.ImportsEdge, "nest:src/users/users.service.ts", "pkg:@nestjs/common"},
		// JavaScript / CommonJS
		{entity.InvokesEdge, legacy + "loadUsers", legacy + "greet"},
		{entity.InvokesEdge, legacy + "loadUsers", legacy + "Counter.inc"},
		{entity.HasFieldEdge, legacy + "Counter", legacy + "Counter.value"},
		{entity.ImportsEdge, "nest:src/legacy.js", "pkg:node:fs"},
	}
	for _, e := range edges {
		if hasEdge(g, e.typ, e.from, e.to) == nil {
			t.Errorf("faltou %s %s → %s", e.typ, e.from, e.to)
		}
	}

	if e := hasEdge(g, entity.InvokesEdge, svc+"UsersService.findOne", logger+"AppLogger.log"); e != nil && e.Resolution != entity.ResolutionInferred {
		t.Errorf("tipo inferido por new deveria gerar resolução inferred, veio %s", e.Resolution)
	}

	c, ok := node(g, ctrl+"UsersController").(entity.Class)
	if !ok || len(c.Decorators) != 1 || c.Decorators[0] != "Controller('users')" {
		t.Errorf("decorators da classe: %+v", c.Decorators)
	}
	m, ok := node(g, ctrl+"UsersController.getUser").(entity.Function)
	if !ok || len(m.Decorators) != 1 || m.Decorators[0] != "Get(':id')" {
		t.Errorf("decorators do método: %+v", m.Decorators)
	}
	p, ok := node(g, ctrl+"UsersController.getUser(id)").(entity.Parameter)
	if !ok || p.TypeName != "string" || len(p.Decorators) != 1 {
		t.Errorf("parâmetro id: %+v", p)
	}
}

func TestWritesAndArguments(t *testing.T) {
	g := extractSample(t)

	var incWrite, fieldArg, doubleArg, readFileCall bool
	for _, e := range g.Edges {
		switch {
		case e.Type == entity.WritesEdge && e.From == legacy+"Counter.inc" && e.To == legacy+"Counter.value":
			incWrite = e.Operator == "+="
		case e.Type == entity.ArgumentEdge && e.To == svc+"UsersService.findOne.user" && e.Member == "email":
			fieldArg = true // this.logger.log(user.email): user tem tipo desconhecido
		case e.Type == entity.ArgumentEdge && e.To == legacy+"double" && e.Index != nil && *e.Index == 0:
			doubleArg = true
		case e.Type == entity.CallsEdge && e.To == "pkg:node:fs" && e.Member == "readFile":
			readFileCall = true
		}
	}
	if !incWrite {
		t.Error("this.value += 1 deveria gerar WRITES{+=} para Counter.value")
	}
	if !fieldArg {
		t.Error("user.email como argumento deveria apontar para a variável user com member email")
	}
	if !doubleArg {
		t.Error("users.map(double) deveria gerar ARGUMENT{0} para a função double")
	}
	if !readFileCall {
		t.Error("readFile(...) deveria gerar CALLS para pkg:node:fs com member readFile")
	}
}

func TestExtractIsSelfContained(t *testing.T) {
	src := []byte(`
import { a } from './a';
export function f(x) {
  const y = a(x);
  return y;
}
`)
	fg, err := javascript.New().Extract(context.Background(), "k:f.ts", source.File{Path: "f.ts", Language: javascript.LangTypeScript, Content: src})
	if err != nil {
		t.Fatal(err)
	}
	if len(fg.Imports) != 1 || fg.Imports[0].Local != "a" || fg.Imports[0].Source != "./a" {
		t.Errorf("imports: %+v", fg.Imports)
	}
	if len(fg.Exports) != 1 || fg.Exports[0].NodeID != "k:f.ts#f" {
		t.Errorf("exports: %+v", fg.Exports)
	}
	var calls, args int
	for _, r := range fg.Refs {
		switch r.Edge {
		case entity.CallsEdge:
			calls++
			if r.Name != "a" {
				t.Errorf("chamada para %q", r.Name)
			}
		case entity.ArgumentEdge:
			args++
		}
	}
	if calls != 1 || args != 1 {
		t.Errorf("refs: %d CALLS, %d ARGUMENT", calls, args)
	}
}
