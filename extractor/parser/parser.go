// Package parser define a extração de um arquivo para o formato intermediário (ir).
// Cada linguagem tem seu subdiretório.
package parser

import (
	"context"
	"fmt"

	"github.com/BMokarzel/rhaxis-code-two/extractor/ir"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source"
)

type Parser interface {
	// Languages são os valores de source.File.Language que o parser aceita
	Languages() []string
	// Extensions mapeia extensão de arquivo para linguagem
	Extensions() map[string]string
	// Extract recebe o ID do file (estável entre extrações) e o arquivo
	Extract(ctx context.Context, fileID string, f source.File) (*ir.FileGraph, error)
}

type Registry struct {
	byLanguage map[string]Parser
	extensions map[string]string
}

func NewRegistry(parsers ...Parser) *Registry {
	r := &Registry{byLanguage: map[string]Parser{}, extensions: map[string]string{}}
	for _, e := range parsers {
		for _, l := range e.Languages() {
			r.byLanguage[l] = e
		}
		for ext, l := range e.Extensions() {
			r.extensions[ext] = l
		}
	}
	return r
}

func (r *Registry) Extensions() map[string]string { return r.extensions }

func (r *Registry) Extract(ctx context.Context, fileID string, f source.File) (*ir.FileGraph, error) {
	e, ok := r.byLanguage[f.Language]
	if !ok {
		return nil, fmt.Errorf("nenhum parser para a linguagem %q", f.Language)
	}
	return e.Extract(ctx, fileID, f)
}
