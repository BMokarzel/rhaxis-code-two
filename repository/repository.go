// Package repository define a persistência do grafo. O orquestrador depende só desta interface.
package repository

import (
	"context"

	"github.com/BMokarzel/rhaxis-code-two/entity"
)

type GraphRepository interface {
	// ReplaceApplication substitui todos os nodes e edges da aplicação pelo grafo informado
	ReplaceApplication(ctx context.Context, app entity.Application, g *entity.Graph) error
}
