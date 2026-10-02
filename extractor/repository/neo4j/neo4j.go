// Package neo4j persiste o grafo em Neo4j. Cada nó vira uma linha com label = NodeType,
// cada edge vira um relacionamento com type = EdgeType. Idempotente por MERGE no id.
package neo4j

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/BMokarzel/rhaxis-code-two/entity"
)

// allowedEdgeTypes é a lista fechada de tipos que aceitamos; usada para validar antes
// de interpolar no Cypher. Como EdgeType é uma constante nossa (não vem de input externo),
// isto é uma defesa em profundidade contra erros de programação.
var allowedEdgeTypes = map[entity.EdgeType]bool{
	entity.ContainsEdge: true, entity.DeclaresEdge: true, entity.HasParameterEdge: true,
	entity.HasFieldEdge: true, entity.HasMethodEdge: true, entity.HasMemberEdge: true,
	entity.ExportsEdge: true, entity.ReadsEdge: true, entity.WritesEdge: true,
	entity.ArgumentEdge: true, entity.ImportsEdge: true, entity.CallsEdge: true,
	entity.InstantiatesEdge: true, entity.HasTypeEdge: true, entity.ReturnsTypeEdge: true,
	entity.ExtendsEdge: true, entity.ImplementsEdge: true, entity.InvokesEdge: true,
	entity.FlowsToEdge: true, entity.NextEdge: true, entity.ExposesEdge: true,
	entity.HandledByEdge: true, entity.HasParamEdge: true, entity.BindsEdge: true,
	entity.RequestsEdge: true, entity.DecoratesEdge: true,
	entity.HasTypeArgEdge: true, entity.ThrowsEdge: true,
}

type Repository struct {
	driver neo4j.DriverWithContext
}

// New abre o driver e verifica conectividade
func New(ctx context.Context, uri, user, password string) (*Repository, error) {
	drv, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(user, password, ""))
	if err != nil {
		return nil, fmt.Errorf("neo4j: abrindo driver: %w", err)
	}
	if err := drv.VerifyConnectivity(ctx); err != nil {
		_ = drv.Close(ctx)
		return nil, fmt.Errorf("neo4j: verificando conectividade: %w", err)
	}
	return &Repository{driver: drv}, nil
}

func (r *Repository) Close(ctx context.Context) error { return r.driver.Close(ctx) }

// ReplaceApplication apaga o subgrafo antigo da aplicação e insere o novo.
// Estratégia:
//  1. Remove nós que pertencem à aplicação (id com prefixo appKey:).
//  2. Cria/atualiza a Application.
//  3. Insere os nós com label = tipo.
//  4. Insere as edges com type = tipo.
func (r *Repository) ReplaceApplication(ctx context.Context, app entity.Application, g *entity.Graph) error {
	session := r.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if err := deleteApp(ctx, tx, app); err != nil {
			return nil, err
		}
		if err := mergeApp(ctx, tx, app); err != nil {
			return nil, err
		}
		if err := insertNodes(ctx, tx, g.Nodes); err != nil {
			return nil, err
		}
		if err := insertEdges(ctx, tx, g.Edges); err != nil {
			return nil, err
		}
		return nil, nil
	})
	return err
}

func deleteApp(ctx context.Context, tx neo4j.ManagedTransaction, app entity.Application) error {
	// Remove todos os nós cujo id começa com "<appKey>:" (nossa convenção de FileID/OwnerID).
	// Application também é apagada para reinserção limpa.
	_, err := tx.Run(ctx,
		`MATCH (n) WHERE n.id STARTS WITH $prefix DETACH DELETE n`,
		map[string]any{"prefix": app.Key + ":"},
	)
	if err != nil {
		return fmt.Errorf("apagando nós do app: %w", err)
	}
	_, err = tx.Run(ctx, `MATCH (a:Application {id: $id}) DETACH DELETE a`, map[string]any{"id": app.ID()})
	if err != nil {
		return fmt.Errorf("apagando application: %w", err)
	}
	return nil
}

func mergeApp(ctx context.Context, tx neo4j.ManagedTransaction, app entity.Application) error {
	props := entity.Properties(app)
	_, err := tx.Run(ctx,
		`MERGE (a:Application {id: $id}) SET a += $props`,
		map[string]any{"id": app.ID(), "props": props},
	)
	if err != nil {
		return fmt.Errorf("merge application: %w", err)
	}
	return nil
}

func insertNodes(ctx context.Context, tx neo4j.ManagedTransaction, nodes []entity.Node) error {
	// Agrupa por label para minimizar chamadas.
	byLabel := map[entity.NodeType][]map[string]any{}
	for _, n := range nodes {
		props := entity.Properties(n)
		byLabel[n.Type()] = append(byLabel[n.Type()], map[string]any{
			"id":    n.ID(),
			"props": props,
		})
	}
	for label, rows := range byLabel {
		// label é uma constante NodeType nossa, interpolação segura.
		query := fmt.Sprintf(
			`UNWIND $rows AS row MERGE (n:%s {id: row.id}) SET n += row.props`,
			string(label),
		)
		if _, err := tx.Run(ctx, query, map[string]any{"rows": rows}); err != nil {
			return fmt.Errorf("inserindo nós %s: %w", label, err)
		}
	}
	return nil
}

func insertEdges(ctx context.Context, tx neo4j.ManagedTransaction, edges []entity.Edge) error {
	// Agrupa por tipo de edge para uma chamada por tipo.
	byType := map[entity.EdgeType][]map[string]any{}
	for _, e := range edges {
		if !allowedEdgeTypes[e.Type] {
			return fmt.Errorf("tipo de edge não suportado: %q", e.Type)
		}
		row := map[string]any{
			"from":  e.From,
			"to":    e.To,
			"props": edgeProps(e),
		}
		byType[e.Type] = append(byType[e.Type], row)
	}
	for etype, rows := range byType {
		query := fmt.Sprintf(
			`UNWIND $rows AS row
			 MATCH (a {id: row.from})
			 MATCH (b {id: row.to})
			 MERGE (a)-[r:%s]->(b)
			 SET r += row.props`,
			string(etype),
		)
		if _, err := tx.Run(ctx, query, map[string]any{"rows": rows}); err != nil {
			return fmt.Errorf("inserindo edges %s: %w", etype, err)
		}
	}
	return nil
}

func edgeProps(e entity.Edge) map[string]any {
	p := map[string]any{}
	if e.Index != nil {
		p["index"] = *e.Index
	}
	if e.Loc != nil {
		p["startLine"] = e.Loc.StartLine
		p["startCol"] = e.Loc.StartCol
		p["endLine"] = e.Loc.EndLine
		p["endCol"] = e.Loc.EndCol
		p["fileId"] = e.Loc.FileID
	}
	if e.Member != "" {
		p["member"] = e.Member
	}
	if e.Operator != "" {
		p["operator"] = e.Operator
	}
	if e.Resolution != "" {
		p["resolution"] = string(e.Resolution)
	}
	return p
}
