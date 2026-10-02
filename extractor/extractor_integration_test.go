// Testa a pipeline completa (parse → link → memory) em duas fixtures que juntas exercitam
// todo NodeType/EdgeType que o parser JS/TS atualmente emite.
package extractor_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor"
	"github.com/BMokarzel/rhaxis-code-two/extractor/linker"
	jsresolver "github.com/BMokarzel/rhaxis-code-two/extractor/linker/javascript"
	"github.com/BMokarzel/rhaxis-code-two/extractor/parser"
	"github.com/BMokarzel/rhaxis-code-two/extractor/parser/javascript"
	"github.com/BMokarzel/rhaxis-code-two/extractor/repository/memory"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source/local"
)

// emittedNodeTypes é o conjunto de NodeType que o parser JS/TS efetivamente produz hoje.
// O teste de cobertura garante que cada um aparece em pelo menos uma fixture.
var emittedNodeTypes = []entity.NodeType{
	entity.ApplicationNode, entity.FileNode, entity.PackageNode, entity.ExternalNode,
	entity.FunctionNode, entity.ParameterNode, entity.VariableNode,
	entity.ClassNode, entity.InterfaceNode, entity.FieldNode,
	entity.EnumNode, entity.EnumMemberNode, entity.CallNode,
	entity.AssignmentNode, entity.JumpNode,
	entity.IfNode, entity.LoopNode, entity.ThrowNode, entity.ReturnNode,
	entity.EndpointNode, entity.HttpParamNode,
}

var emittedEdgeTypes = []entity.EdgeType{
	entity.ContainsEdge, entity.DeclaresEdge,
	entity.HasParameterEdge, entity.HasFieldEdge, entity.HasMethodEdge, entity.HasMemberEdge,
	entity.ExportsEdge, entity.ImportsEdge, entity.BindsEdge,
	entity.ReadsEdge, entity.WritesEdge, entity.ArgumentEdge,
	entity.CallsEdge, entity.InstantiatesEdge,
	entity.HasTypeEdge, entity.HasTypeArgEdge, entity.ReturnsTypeEdge,
	entity.ExtendsEdge, entity.ImplementsEdge,
	entity.InvokesEdge, entity.ThrowsEdge, entity.NextEdge, entity.FlowsToEdge,
	entity.DecoratesEdge, entity.ExposesEdge, entity.HandledByEdge, entity.HasParamEdge,
}

type fixture struct {
	name string
	key  string
	path string
	// Only-covers: NodeTypes/EdgeTypes que esta fixture individualmente deve produzir.
	// Não exige contagens exatas para não travar o teste em ajustes futuros do parser.
	mustHaveNodes []entity.NodeType
	mustHaveEdges []entity.EdgeType
}

func fixtures() []fixture {
	return []fixture{
		{
			name: "fixture-a-nest",
			key:  "fixture-a",
			path: filepath.Join("..", "testdata", "fixture-a-nest"),
			mustHaveNodes: []entity.NodeType{
				entity.FileNode, entity.PackageNode,
				entity.ClassNode, entity.InterfaceNode, entity.EnumNode, entity.EnumMemberNode,
				entity.FunctionNode, entity.ParameterNode, entity.VariableNode,
				entity.FieldNode, entity.CallNode,
				entity.IfNode, entity.ThrowNode, entity.ReturnNode,
				entity.EndpointNode, entity.HttpParamNode,
			},
			mustHaveEdges: []entity.EdgeType{
				entity.ContainsEdge, entity.DeclaresEdge,
				entity.HasParameterEdge, entity.HasFieldEdge, entity.HasMethodEdge, entity.HasMemberEdge,
				entity.ExportsEdge, entity.ImportsEdge, entity.BindsEdge,
				entity.HasTypeEdge, entity.ReturnsTypeEdge,
				entity.ExtendsEdge, entity.ImplementsEdge,
				entity.CallsEdge, entity.InstantiatesEdge,
				entity.InvokesEdge,
			},
		},
		{
			name: "fixture-b-cli",
			key:  "fixture-b",
			path: filepath.Join("..", "testdata", "fixture-b-cli"),
			mustHaveNodes: []entity.NodeType{
				entity.FileNode, entity.PackageNode, entity.ExternalNode,
				entity.ClassNode, entity.FunctionNode, entity.ParameterNode, entity.VariableNode,
				entity.CallNode, entity.AssignmentNode, entity.JumpNode,
				entity.IfNode, entity.LoopNode, entity.ReturnNode,
			},
			mustHaveEdges: []entity.EdgeType{
				entity.ContainsEdge, entity.DeclaresEdge, entity.HasParameterEdge,
				entity.ExportsEdge, entity.ImportsEdge, entity.BindsEdge,
				entity.ReadsEdge, entity.WritesEdge, entity.ArgumentEdge,
				entity.CallsEdge, entity.InstantiatesEdge, entity.NextEdge, entity.FlowsToEdge,
				entity.ExtendsEdge, entity.HasMethodEdge,
			},
		},
	}
}

func extractFixture(t *testing.T, f fixture) *entity.Graph {
	t.Helper()
	registry := parser.NewRegistry(javascript.New())
	js := jsresolver.NewFactory()
	repo := memory.New()
	app := entity.Application{Base: entity.Base{NodeID: "app:" + f.key}, Name: f.key, Key: f.key}
	o := &extractor.Extractor{
		Parser: registry,
		Linker: linker.New(map[string]linker.ResolverFactory{
			javascript.LangJavaScript: js,
			javascript.LangTypeScript: js,
			javascript.LangTSX:        js,
		}),
		Repository: repo,
	}
	report, err := o.Run(context.Background(), app, local.New(f.path, registry.Extensions()))
	if err != nil {
		t.Fatalf("extract %s: %v", f.name, err)
	}
	for _, fe := range report.Failed {
		t.Errorf("%s: falha em %s: %v", f.name, fe.Path, fe.Err)
	}
	if report.Files == 0 {
		t.Fatalf("%s: nenhum arquivo processado (path=%s)", f.name, f.path)
	}
	g := repo.Graph(app.ID())
	if g == nil {
		t.Fatalf("%s: grafo vazio", f.name)
	}
	return g
}

func countNodes(g *entity.Graph) map[entity.NodeType]int {
	m := map[entity.NodeType]int{}
	for _, n := range g.Nodes {
		m[n.Type()]++
	}
	return m
}

func countEdges(g *entity.Graph) map[entity.EdgeType]int {
	m := map[entity.EdgeType]int{}
	for _, e := range g.Edges {
		m[e.Type]++
	}
	return m
}

func TestFixtures(t *testing.T) {
	// nodeCoverage[type] = fixtures em que apareceu
	nodeCoverage := map[entity.NodeType][]string{}
	edgeCoverage := map[entity.EdgeType][]string{}

	for _, f := range fixtures() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			g := extractFixture(t, f)
			nodes := countNodes(g)
			edges := countEdges(g)

			t.Logf("nodes=%d edges=%d", len(g.Nodes), len(g.Edges))
			for typ, n := range nodes {
				t.Logf("  node %-12s = %d", typ, n)
				nodeCoverage[typ] = append(nodeCoverage[typ], f.name)
			}
			for typ, n := range edges {
				t.Logf("  edge %-14s = %d", typ, n)
				edgeCoverage[typ] = append(edgeCoverage[typ], f.name)
			}

			for _, want := range f.mustHaveNodes {
				if nodes[want] == 0 {
					t.Errorf("esperava pelo menos 1 node do tipo %q, achou 0", want)
				}
			}
			for _, want := range f.mustHaveEdges {
				if edges[want] == 0 {
					t.Errorf("esperava pelo menos 1 edge do tipo %q, achou 0", want)
				}
			}
		})
	}

	t.Run("coverage_all_node_types", func(t *testing.T) {
		for _, typ := range emittedNodeTypes {
			if len(nodeCoverage[typ]) == 0 {
				t.Errorf("nenhuma fixture produziu node do tipo %q (esperado no conjunto de tipos emitidos)", typ)
			}
		}
	})

	t.Run("coverage_all_edge_types", func(t *testing.T) {
		for _, typ := range emittedEdgeTypes {
			if len(edgeCoverage[typ]) == 0 {
				t.Errorf("nenhuma fixture produziu edge do tipo %q (esperado no conjunto de tipos emitidos)", typ)
			}
		}
	})
}

func TestFixtureAKeyRelations(t *testing.T) {
	f := fixtures()[0] // fixture-a-nest
	g := extractFixture(t, f)

	// User EXTENDS BaseEntity
	if !hasEdgeByNames(g, entity.ExtendsEdge, "User", "BaseEntity") {
		t.Error("esperava EXTENDS de User → BaseEntity")
	}
	// AppLogger EXTENDS BaseLogger
	if !hasEdgeByNames(g, entity.ExtendsEdge, "AppLogger", "BaseLogger") {
		t.Error("esperava EXTENDS de AppLogger → BaseLogger")
	}
	// BaseLogger IMPLEMENTS ILogger
	if !hasEdgeByNames(g, entity.ImplementsEdge, "BaseLogger", "ILogger") {
		t.Error("esperava IMPLEMENTS de BaseLogger → ILogger")
	}
	// UserStatus tem membros
	edges := countEdges(g)
	if edges[entity.HasMemberEdge] < 3 {
		t.Errorf("esperava >=3 HAS_MEMBER (enum UserStatus), achou %d", edges[entity.HasMemberEdge])
	}
}

func TestFixtureBKeyRelations(t *testing.T) {
	f := fixtures()[1] // fixture-b-cli
	g := extractFixture(t, f)

	if !hasEdgeByNames(g, entity.ExtendsEdge, "LRUCache", "Store") {
		t.Error("esperava EXTENDS de LRUCache → Store")
	}
	// require('node:fs/promises') resolve para o package "node:fs" no linker.
	if !hasNodeNamed(g, entity.PackageNode, "node:fs") {
		t.Errorf("esperava Package node:fs")
	}
}

func hasEdgeByNames(g *entity.Graph, typ entity.EdgeType, fromName, toName string) bool {
	nameByID := map[string]string{}
	for _, n := range g.Nodes {
		if named, ok := extractName(n); ok {
			nameByID[n.ID()] = named
		}
	}
	for _, e := range g.Edges {
		if e.Type != typ {
			continue
		}
		if nameByID[e.From] == fromName && nameByID[e.To] == toName {
			return true
		}
	}
	return false
}

func hasNodeNamed(g *entity.Graph, typ entity.NodeType, name string) bool {
	for _, n := range g.Nodes {
		if n.Type() != typ {
			continue
		}
		if got, ok := extractName(n); ok && got == name {
			return true
		}
	}
	return false
}

func extractName(n entity.Node) (string, bool) {
	// entity.Properties achata o node em map[string]any e inclui "name" quando existe.
	props := entity.Properties(n)
	if v, ok := props["name"].(string); ok {
		return v, true
	}
	return "", false
}
