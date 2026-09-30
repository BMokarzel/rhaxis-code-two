package linker_test

import (
	"context"
	"testing"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/ir"
	"github.com/BMokarzel/rhaxis-code-two/linker"
	"github.com/BMokarzel/rhaxis-code-two/linker/javascript"
)

func base(id string) entity.CodeBase { return entity.CodeBase{Base: entity.Base{NodeID: id}} }

// Simula o que o extrator gera para:
//
//	// src/users/users.service.ts
//	export class UsersService { findOne(id) { ... } }
//
//	// src/users/index.ts
//	export * from './users.service'
//
//	// src/users/users.controller.ts
//	import { Get } from '@nestjs/common'
//	import { UsersService } from '.'
//	export class UsersController {
//	  constructor(private readonly usersService: UsersService) {}
//	  getUser(id) { console.log(id); return this.usersService.findOne(id) }
//	}
func fixture() []*ir.FileGraph {
	svc := &ir.FileGraph{
		File:        entity.File{Base: entity.Base{NodeID: "a:svc"}, Path: "src/users/users.service.ts", Language: "typescript"},
		FileScopeID: "svc#s0",
		Nodes: []entity.Node{
			entity.Class{CodeBase: base("svc#UsersService"), Name: "UsersService"},
			entity.Function{CodeBase: base("svc#UsersService.findOne"), Name: "findOne", Kind: entity.FunctionMethod},
		},
		Edges: []entity.Edge{
			{Type: entity.HasMethodEdge, From: "svc#UsersService", To: "svc#UsersService.findOne"},
		},
		Scopes:  []ir.Scope{{ID: "svc#s0", Names: map[string]string{"UsersService": "svc#UsersService"}}},
		Exports: []ir.Export{{Kind: ir.ExportLocal, Name: "UsersService", NodeID: "svc#UsersService"}},
	}
	index := &ir.FileGraph{
		File:        entity.File{Base: entity.Base{NodeID: "a:index"}, Path: "src/users/index.ts", Language: "typescript"},
		FileScopeID: "index#s0",
		Scopes:      []ir.Scope{{ID: "index#s0", Names: map[string]string{}}},
		Exports:     []ir.Export{{Kind: ir.ExportReexportAll, Source: "./users.service"}},
	}
	ctrl := &ir.FileGraph{
		File:        entity.File{Base: entity.Base{NodeID: "a:ctrl"}, Path: "src/users/users.controller.ts", Language: "typescript"},
		FileScopeID: "ctrl#s0",
		Nodes: []entity.Node{
			entity.Class{CodeBase: base("ctrl#UsersController"), Name: "UsersController"},
			entity.Field{CodeBase: base("ctrl#UsersController.usersService"), Name: "usersService"},
			entity.Function{CodeBase: base("ctrl#UsersController.getUser"), Name: "getUser", Kind: entity.FunctionMethod},
			entity.Parameter{CodeBase: base("ctrl#UsersController.getUser(id)"), Name: "id"},
			entity.Call{CodeBase: entity.CodeBase{Base: entity.Base{NodeID: "ctrl#call1"}, OwnerID: "ctrl#UsersController.getUser"}},
			entity.Call{CodeBase: entity.CodeBase{Base: entity.Base{NodeID: "ctrl#call2"}, OwnerID: "ctrl#UsersController.getUser"}},
		},
		Edges: []entity.Edge{
			{Type: entity.HasFieldEdge, From: "ctrl#UsersController", To: "ctrl#UsersController.usersService"},
			{Type: entity.HasMethodEdge, From: "ctrl#UsersController", To: "ctrl#UsersController.getUser"},
		},
		Scopes: []ir.Scope{
			{ID: "ctrl#s0", Names: map[string]string{"UsersController": "ctrl#UsersController"}},
			{ID: "ctrl#class", ParentID: "ctrl#s0", Names: map[string]string{"this": "ctrl#UsersController"}},
			{ID: "ctrl#getUser", ParentID: "ctrl#class", Names: map[string]string{"id": "ctrl#UsersController.getUser(id)"}},
		},
		Imports: []ir.Import{
			{Local: "Get", Imported: "Get", Source: "@nestjs/common", Kind: ir.ImportNamed},
			{Local: "UsersService", Imported: "UsersService", Source: ".", Kind: ir.ImportNamed},
		},
		Refs: []ir.PendingRef{
			{From: "ctrl#UsersController.usersService", Edge: entity.HasTypeEdge, ScopeID: "ctrl#class", Name: "UsersService"},
			{From: "ctrl#call1", Edge: entity.CallsEdge, ScopeID: "ctrl#getUser", Name: "console", Path: []string{"log"}},
			{From: "ctrl#call1", Edge: entity.ArgumentEdge, ScopeID: "ctrl#getUser", Name: "id", Index: entity.IntPtr(0)},
			{From: "ctrl#call2", Edge: entity.CallsEdge, ScopeID: "ctrl#getUser", Name: "this", Path: []string{"usersService", "findOne"}},
			{From: "ctrl#call2", Edge: entity.ArgumentEdge, ScopeID: "ctrl#getUser", Name: "id", Index: entity.IntPtr(0)},
			{From: "ctrl#UsersController.getUser", Edge: entity.ReadsEdge, ScopeID: "ctrl#getUser", Name: "Get"},
		},
	}
	return []*ir.FileGraph{svc, index, ctrl}
}

func link(t *testing.T) *entity.Graph {
	t.Helper()
	js := javascript.NewFactory()
	l := linker.New(map[string]linker.ResolverFactory{"typescript": js})
	app := entity.Application{Base: entity.Base{NodeID: "app:a"}, Key: "a"}
	g, err := l.Link(context.Background(), linker.Input{App: app, Files: fixture()})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func findEdge(g *entity.Graph, typ entity.EdgeType, from, to string) *entity.Edge {
	for i, e := range g.Edges {
		if e.Type == typ && e.From == from && e.To == to {
			return &g.Edges[i]
		}
	}
	return nil
}

func TestLinkAcrossFiles(t *testing.T) {
	g := link(t)
	want := []struct {
		typ      entity.EdgeType
		from, to string
		member   string
	}{
		{entity.HasTypeEdge, "ctrl#UsersController.usersService", "svc#UsersService", ""},
		{entity.CallsEdge, "ctrl#call2", "svc#UsersService.findOne", ""},
		{entity.ReadsEdge, "ctrl#call2", "ctrl#UsersController.usersService", ""},
		{entity.ArgumentEdge, "ctrl#call2", "ctrl#UsersController.getUser(id)", ""},
		{entity.CallsEdge, "ctrl#call1", "app:a/ext:console", "log"},
		{entity.ReadsEdge, "ctrl#UsersController.getUser", "pkg:@nestjs/common", "Get"},
		{entity.InvokesEdge, "ctrl#UsersController.getUser", "svc#UsersService.findOne", ""},
		{entity.ImportsEdge, "a:ctrl", "a:index", ""},
		{entity.ImportsEdge, "a:ctrl", "pkg:@nestjs/common", ""},
		{entity.ContainsEdge, "app:a", "a:svc", ""},
	}
	for _, w := range want {
		e := findEdge(g, w.typ, w.from, w.to)
		if e == nil {
			t.Errorf("faltou %s %s → %s", w.typ, w.from, w.to)
			continue
		}
		if e.Member != w.member {
			t.Errorf("%s %s → %s: member %q, quer %q", w.typ, w.from, w.to, e.Member, w.member)
		}
	}
	if e := findEdge(g, entity.CallsEdge, "ctrl#call2", "svc#UsersService.findOne"); e != nil && e.Resolution != entity.ResolutionExact {
		t.Errorf("resolução de this.usersService.findOne = %s, quer exact", e.Resolution)
	}
}

func TestUnknownMemberBecomesRead(t *testing.T) {
	files := fixture()
	ctrl := files[2]
	// sem o tipo do campo, this.usersService.findOne() não tem alvo conhecido
	ctrl.Refs = ctrl.Refs[1:]
	l := linker.New(map[string]linker.ResolverFactory{"typescript": javascript.NewFactory()})
	g, err := l.Link(context.Background(), linker.Input{App: entity.Application{Base: entity.Base{NodeID: "app:a"}}, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	e := findEdge(g, entity.ReadsEdge, "ctrl#call2", "ctrl#UsersController.usersService")
	if e == nil || e.Member != "findOne" {
		t.Fatalf("esperava READS para o campo com member findOne, veio %+v", e)
	}
	if findEdge(g, entity.CallsEdge, "ctrl#call2", "ctrl#UsersController.usersService") != nil {
		t.Error("não deveria haver CALLS para um campo")
	}
}
