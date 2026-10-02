package neo4j

import (
	"context"
	"fmt"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"

	"github.com/BMokarzel/rhaxis-code-two/entity"
)

// LoadApplications devolve todas as Applications do grafo, com os sinais já
// denormalizados (Endpoints/EnvVarsRead/HostCandidates). Usado pelo comando
// link-services para enxergar o universo de apps antes de ligar.
func (r *Repository) LoadApplications(ctx context.Context) ([]entity.Application, error) {
	session := r.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)
	res, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx,
			`MATCH (a:Application) RETURN a.id AS id, a.name AS name, a.key AS key,
			 coalesce(a.endpoints, []) AS endpoints,
			 coalesce(a.envVarsRead, []) AS envVarsRead,
			 coalesce(a.hostCandidates, []) AS hostCandidates`,
			nil)
		if err != nil {
			return nil, err
		}
		var apps []entity.Application
		for result.Next(ctx) {
			rec := result.Record()
			app := entity.Application{
				Base: entity.Base{NodeID: asString(rec.AsMap()["id"])},
				Name: asString(rec.AsMap()["name"]),
				Key:  asString(rec.AsMap()["key"]),
			}
			app.Endpoints = asStringSlice(rec.AsMap()["endpoints"])
			app.EnvVarsRead = asStringSlice(rec.AsMap()["envVarsRead"])
			app.HostCandidates = asStringSlice(rec.AsMap()["hostCandidates"])
			apps = append(apps, app)
		}
		return apps, result.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("neo4j: carregando applications: %w", err)
	}
	return res.([]entity.Application), nil
}

// ReplaceCrossServiceLinks substitui, para a app `srcAppID`, todas as
// REQUESTS/PRODUCES/CONSUMES anteriores e todos os LinkReview com Status=pending
// (confirmed/overridden são preservados). Insere as novas edges e reviews.
// Idempotente: rodar duas vezes com o mesmo input gera o mesmo grafo.
func (r *Repository) ReplaceCrossServiceLinks(ctx context.Context, srcAppID string, edges []entity.Edge, reviews []entity.LinkReview) error {
	session := r.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		// 1. Remove edges REQUESTS/PRODUCES/CONSUMES emitidas pela app
		_, err := tx.Run(ctx,
			`MATCH (src {id: $id})-[r:REQUESTS|PRODUCES|CONSUMES]->() DELETE r`,
			map[string]any{"id": srcAppID})
		if err != nil {
			return nil, fmt.Errorf("limpando edges cross-service: %w", err)
		}
		// 2. Remove LinkReview pending desta app (preserva confirmed/overridden/rejected)
		_, err = tx.Run(ctx,
			`MATCH (lr:LinkReview) WHERE lr.id STARTS WITH $prefix AND lr.status = 'pending'
			 DETACH DELETE lr`,
			map[string]any{"prefix": appKeyFromID(srcAppID) + ":review:"})
		if err != nil {
			return nil, fmt.Errorf("limpando reviews pending: %w", err)
		}
		// 3. Insere novas edges
		if err := insertEdges(ctx, tx, edges); err != nil {
			return nil, err
		}
		// 4. Insere novos LinkReview — merge pelo ID (determinístico)
		for _, lr := range reviews {
			if err := upsertReview(ctx, tx, lr); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

// upsertReview preserva o Status de um review existente se não for "pending".
// Isso garante que reruns do linker não sobrescrevam uma decisão humana prévia.
func upsertReview(ctx context.Context, tx neo4j.ManagedTransaction, lr entity.LinkReview) error {
	props := entity.Properties(lr)
	_, err := tx.Run(ctx,
		`MERGE (lr:LinkReview {id: $id})
		 ON CREATE SET lr += $props
		 ON MATCH SET lr.chosenId = $props.chosenId,
		              lr.candidateTargets = $props.candidateTargets,
		              lr.candidateScores = $props.candidateScores,
		              lr.candidateWhys = $props.candidateWhys,
		              lr.hint = $props.hint`,
		map[string]any{"id": lr.ID(), "props": props},
	)
	if err != nil {
		return fmt.Errorf("upsert review %s: %w", lr.ID(), err)
	}
	return nil
}

// appKeyFromID extrai a key do app a partir do ID "app:<key>".
func appKeyFromID(id string) string {
	const prefix = "app:"
	if len(id) > len(prefix) && id[:len(prefix)] == prefix {
		return id[len(prefix):]
	}
	return id
}

// ListPendingReviews devolve todos os LinkReview com Status=pending — base do
// comando `link-services review`.
func (r *Repository) ListPendingReviews(ctx context.Context) ([]entity.LinkReview, error) {
	session := r.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)
	res, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx,
			`MATCH (lr:LinkReview {status: 'pending'})
			 RETURN lr.id AS id, lr.callId AS callId, lr.edgeType AS edgeType,
			        lr.chosenId AS chosenId, lr.hint AS hint,
			        coalesce(lr.candidateTargets, []) AS targets,
			        coalesce(lr.candidateScores, []) AS scores,
			        coalesce(lr.candidateWhys, []) AS whys
			 ORDER BY lr.id`,
			nil)
		if err != nil {
			return nil, err
		}
		var out []entity.LinkReview
		for result.Next(ctx) {
			m := result.Record().AsMap()
			out = append(out, entity.LinkReview{
				Base:             entity.Base{NodeID: asString(m["id"])},
				CallID:           asString(m["callId"]),
				EdgeType:         entity.EdgeType(asString(m["edgeType"])),
				ChosenID:         asString(m["chosenId"]),
				Hint:             asString(m["hint"]),
				CandidateTargets: asStringSlice(m["targets"]),
				CandidateScores:  asIntSlice(m["scores"]),
				CandidateWhys:    asStringSlice(m["whys"]),
				Status:           "pending",
			})
		}
		return out, result.Err()
	})
	if err != nil {
		return nil, err
	}
	return res.([]entity.LinkReview), nil
}

// SetReviewStatus muda o Status de um review (confirmed/overridden/rejected).
// Se o novo status for "overridden", o chamador também muda a edge REQUESTS
// correspondente — essa parte é opt-in via UpdateRequestsTarget.
func (r *Repository) SetReviewStatus(ctx context.Context, reviewID, status string) error {
	session := r.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx,
			`MATCH (lr:LinkReview {id: $id}) SET lr.status = $status`,
			map[string]any{"id": reviewID, "status": status})
		return nil, err
	})
	return err
}

// UpdateRequestsTarget muda o `To` da edge REQUESTS que saiu de `fromID` com
// o Member/Kind do review. Usado por `link-services override`.
func (r *Repository) UpdateRequestsTarget(ctx context.Context, fromID, oldToID, newToID string) error {
	session := r.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx,
			`MATCH (src {id: $from})-[r:REQUESTS]->(old {id: $oldTo})
			 MATCH (new {id: $newTo})
			 DELETE r
			 MERGE (src)-[nr:REQUESTS]->(new)
			 SET nr.resolution = 'overridden'`,
			map[string]any{"from": fromID, "oldTo": oldToID, "newTo": newToID})
		return nil, err
	})
	return err
}

// DeleteRequestsEdge remove a edge REQUESTS entre dois nodes — base do
// `link-services reject`.
func (r *Repository) DeleteRequestsEdge(ctx context.Context, fromID, toID string) error {
	session := r.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx,
			`MATCH (src {id: $from})-[r:REQUESTS]->(dst {id: $to}) DELETE r`,
			map[string]any{"from": fromID, "to": toID})
		return nil, err
	})
	return err
}

// AuditUnlinkedExternals devolve chamadas que caíram em `External` HTTP-ish e
// não têm REQUESTS ligada — base do `link-services audit`.
type AuditItem struct {
	AppID        string
	CallSiteID   string
	CalleeText   string
	FileID       string
	StartLine    int
}

func (r *Repository) AuditUnlinkedExternals(ctx context.Context) ([]AuditItem, error) {
	session := r.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)
	res, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		// Externals cujo nome é um client HTTP conhecido; sem REQUESTS saindo da app.
		result, err := tx.Run(ctx,
			`MATCH (app:Application)-[:CONTAINS]->(:File)-[:DECLARES]->(fn)
			 MATCH (c:Call)-[:CALLS]->(ext:External)
			 WHERE ext.name IN ['fetch', 'axios', 'request', 'got', 'http']
			   AND c.ownerId = fn.id
			   AND NOT EXISTS { MATCH (app)-[:REQUESTS]->() }
			 RETURN app.id AS appId, c.id AS callId, c.calleeText AS callee,
			        c.fileId AS fileId, c.startLine AS startLine
			 LIMIT 500`,
			nil)
		if err != nil {
			return nil, err
		}
		var out []AuditItem
		for result.Next(ctx) {
			m := result.Record().AsMap()
			out = append(out, AuditItem{
				AppID:      asString(m["appId"]),
				CallSiteID: asString(m["callId"]),
				CalleeText: asString(m["callee"]),
				FileID:     asString(m["fileId"]),
				StartLine:  asInt(m["startLine"]),
			})
		}
		return out, result.Err()
	})
	if err != nil {
		return nil, err
	}
	return res.([]AuditItem), nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any) int {
	switch x := v.(type) {
	case int64:
		return int(x)
	case int:
		return x
	case float64:
		return int(x)
	}
	return 0
}

func asStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asIntSlice(v any) []int {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(raw))
	for _, e := range raw {
		out = append(out, asInt(e))
	}
	return out
}
