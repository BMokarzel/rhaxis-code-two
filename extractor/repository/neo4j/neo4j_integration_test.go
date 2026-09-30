// Teste de integração com Neo4j.
//
// Requer NEO4J_URI setado (via .env + docker compose up neo4j). Sem essa env o teste é ignorado,
// então "go test ./..." roda offline normal.
package neo4j_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor"
	"github.com/BMokarzel/rhaxis-code-two/extractor/linker"
	jsresolver "github.com/BMokarzel/rhaxis-code-two/extractor/linker/javascript"
	"github.com/BMokarzel/rhaxis-code-two/extractor/parser"
	"github.com/BMokarzel/rhaxis-code-two/extractor/parser/javascript"
	"github.com/BMokarzel/rhaxis-code-two/extractor/repository/memory"
	neo4jrepo "github.com/BMokarzel/rhaxis-code-two/extractor/repository/neo4j"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source/local"
)

func neo4jConfig(t *testing.T) (uri, user, pass string) {
	t.Helper()
	uri = os.Getenv("NEO4J_URI")
	if uri == "" {
		t.Skip("NEO4J_URI não setado; pulando teste de integração Neo4j")
	}
	user = os.Getenv("NEO4J_USER")
	if user == "" {
		user = "neo4j"
	}
	pass = os.Getenv("NEO4J_PASSWORD")
	if pass == "" {
		pass = "neo4j"
	}
	return
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	// este arquivo: extractor/repository/neo4j/neo4j_integration_test.go
	// testdata:     testdata/<name>
	return filepath.Join("..", "..", "..", "testdata", name)
}

// extractGraph roda o pipeline em memória para obter o grafo esperado.
func extractGraph(t *testing.T, key, path string) (entity.Application, *entity.Graph) {
	t.Helper()
	registry := parser.NewRegistry(javascript.New())
	js := jsresolver.NewFactory()
	repo := memory.New()
	app := entity.Application{Base: entity.Base{NodeID: "app:" + key}, Name: key, Key: key}
	o := &extractor.Extractor{
		Parser: registry,
		Linker: linker.New(map[string]linker.ResolverFactory{
			javascript.LangJavaScript: js,
			javascript.LangTypeScript: js,
			javascript.LangTSX:        js,
		}),
		Repository: repo,
	}
	report, err := o.Run(context.Background(), app, local.New(path, registry.Extensions()))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if report.Files == 0 {
		t.Fatalf("nenhum arquivo processado em %s", path)
	}
	return app, repo.Graph(app.ID())
}

func TestNeo4jPersistFixtureA(t *testing.T) {
	uri, user, pass := neo4jConfig(t)
	ctx := context.Background()

	app, g := extractGraph(t, "fixture-a-neo4j-test", fixturePath(t, "fixture-a-nest"))

	repo, err := neo4jrepo.New(ctx, uri, user, pass)
	if err != nil {
		t.Fatalf("conectando neo4j: %v", err)
	}
	defer repo.Close(ctx)

	// Persiste
	if err := repo.ReplaceApplication(ctx, app, g); err != nil {
		t.Fatalf("persistir: %v", err)
	}
	// t.Cleanup remove após o teste
	t.Cleanup(func() {
		drv, _ := neo4jdriver.NewDriverWithContext(uri, neo4jdriver.BasicAuth(user, pass, ""))
		defer drv.Close(ctx)
		sess := drv.NewSession(ctx, neo4jdriver.SessionConfig{AccessMode: neo4jdriver.AccessModeWrite})
		defer sess.Close(ctx)
		_, _ = sess.ExecuteWrite(ctx, func(tx neo4jdriver.ManagedTransaction) (any, error) {
			_, _ = tx.Run(ctx, `MATCH (n) WHERE n.id STARTS WITH $prefix DETACH DELETE n`, map[string]any{"prefix": app.Key + ":"})
			_, _ = tx.Run(ctx, `MATCH (a:Application {id:$id}) DETACH DELETE a`, map[string]any{"id": app.ID()})
			return nil, nil
		})
	})

	// Conta nós persistidos e compara com o grafo em memória.
	drv, err := neo4jdriver.NewDriverWithContext(uri, neo4jdriver.BasicAuth(user, pass, ""))
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	defer drv.Close(ctx)
	sess := drv.NewSession(ctx, neo4jdriver.SessionConfig{AccessMode: neo4jdriver.AccessModeRead})
	defer sess.Close(ctx)

	// Coleta IDs do grafo em memória. Os IDs cobrem todos os namespaces
	// (fixture-a:..., app:fixture-a/ext:..., pkg:node:fs, etc.), então uma
	// única query com WHERE n.id IN $ids valida todos os nós persistidos.
	nodeIDs := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeIDs = append(nodeIDs, n.ID())
	}
	nodesInDB := scalarInt(t, ctx, sess,
		`MATCH (n) WHERE n.id IN $ids RETURN count(n) AS c`,
		map[string]any{"ids": nodeIDs})
	if nodesInDB != len(g.Nodes) {
		t.Errorf("nodes: db=%d, memory=%d", nodesInDB, len(g.Nodes))
	}

	// Application separada
	appCount := scalarInt(t, ctx, sess,
		`MATCH (a:Application {id:$id}) RETURN count(a) AS c`,
		map[string]any{"id": app.ID()})
	if appCount != 1 {
		t.Errorf("Application count = %d, esperava 1", appCount)
	}

	// Edges emanando de nós deste app (Application ou qualquer node do grafo).
	// Não usar b.id porque Packages são compartilhados entre apps — edges de outras
	// extrações apontando para o mesmo Package apareceriam duplicadas.
	// O MERGE do repositório deduplica por (from, to, type), então compare com o
	// conjunto distinto do grafo em memória.
	type edgeKey struct{ from, to, typ string }
	distinct := map[edgeKey]struct{}{}
	for _, e := range g.Edges {
		distinct[edgeKey{e.From, e.To, string(e.Type)}] = struct{}{}
	}
	edgesInDB := scalarInt(t, ctx, sess,
		`MATCH (a)-[r]->(b)
		 WHERE a.id IN $ids OR a.id = $appID
		 RETURN count(r) AS c`,
		map[string]any{"ids": nodeIDs, "appID": app.ID()})
	if edgesInDB != len(distinct) {
		t.Errorf("edges: db=%d, memory=%d (distinct=%d, total=%d)",
			edgesInDB, len(g.Edges), len(distinct), len(g.Edges))
	}

	// Idempotência: persistir de novo com o mesmo grafo não deve duplicar
	if err := repo.ReplaceApplication(ctx, app, g); err != nil {
		t.Fatalf("re-persist: %v", err)
	}
	nodesAgain := scalarInt(t, ctx, sess,
		`MATCH (n) WHERE n.id IN $ids RETURN count(n) AS c`,
		map[string]any{"ids": nodeIDs})
	if nodesAgain != nodesInDB {
		t.Errorf("idempotência quebrada: nodes=%d (era %d)", nodesAgain, nodesInDB)
	}

	// Sanity: existe pelo menos uma edge EXTENDS entre duas Class do app
	extendsCount := scalarInt(t, ctx, sess,
		fmt.Sprintf(`MATCH (a:Class)-[r:%s]->(b:Class) WHERE a.id STARTS WITH $prefix RETURN count(r) AS c`, entity.ExtendsEdge),
		map[string]any{"prefix": app.Key + ":"})
	if extendsCount == 0 {
		t.Error("esperava pelo menos 1 EXTENDS entre classes no app")
	}
}

func scalarInt(t *testing.T, ctx context.Context, sess neo4jdriver.SessionWithContext, query string, params map[string]any) int {
	t.Helper()
	result, err := sess.ExecuteRead(ctx, func(tx neo4jdriver.ManagedTransaction) (any, error) {
		r, err := tx.Run(ctx, query, params)
		if err != nil {
			return nil, err
		}
		rec, err := r.Single(ctx)
		if err != nil {
			return nil, err
		}
		return rec.Values[0], nil
	})
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	switch v := result.(type) {
	case int64:
		return int(v)
	case int:
		return v
	default:
		t.Fatalf("valor inesperado %T", result)
		return 0
	}
}
