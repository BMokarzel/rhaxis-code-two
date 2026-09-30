// Package extractor define a extração de um arquivo para o formato intermediário (ir).
// Cada linguagem tem seu subdiretório.
package extractor

import (
	"context"
	"fmt"

	"github.com/BMokarzel/rhaxis-code-two/ir"
	"github.com/BMokarzel/rhaxis-code-two/source"
)

type Extractor interface {
	// Languages são os valores de source.File.Language que o extrator aceita
	Languages() []string
	// Extensions mapeia extensão de arquivo para linguagem
	Extensions() map[string]string
	// Extract recebe o ID do file (estável entre extrações) e o arquivo
	Extract(ctx context.Context, fileID string, f source.File) (*ir.FileGraph, error)
}

type Registry struct {
	byLanguage map[string]Extractor
	extensions map[string]string
}

func NewRegistry(extractors ...Extractor) *Registry {
	r := &Registry{byLanguage: map[string]Extractor{}, extensions: map[string]string{}}
	for _, e := range extractors {
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
		return nil, fmt.Errorf("nenhum extrator para a linguagem %q", f.Language)
	}
	return e.Extract(ctx, fileID, f)
}
