// Package memory guarda o grafo em memória. Serve para testes e para inspecionar a extração em JSON.
package memory

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/BMokarzel/rhaxis-code-two/entity"
)

type Repository struct {
	mu   sync.RWMutex
	apps map[string]*entity.Graph
}

func New() *Repository {
	return &Repository{apps: map[string]*entity.Graph{}}
}

func (r *Repository) ReplaceApplication(_ context.Context, app entity.Application, g *entity.Graph) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.apps[app.ID()] = g
	return nil
}

func (r *Repository) Graph(appID string) *entity.Graph {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.apps[appID]
}

type dump struct {
	Nodes []map[string]any `json:"nodes"`
	Edges []entity.Edge    `json:"edges"`
}

// WriteJSON escreve o grafo da aplicação com cada node achatado em propriedades
func (r *Repository) WriteJSON(w io.Writer, appID string) error {
	g := r.Graph(appID)
	d := dump{Nodes: []map[string]any{}, Edges: []entity.Edge{}}
	if g != nil {
		for _, n := range g.Nodes {
			d.Nodes = append(d.Nodes, entity.Properties(n))
		}
		d.Edges = append(d.Edges, g.Edges...)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(d)
}
