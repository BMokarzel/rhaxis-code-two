// Package extractor é a aplicação de extração: captura → parsing → linkagem → persistência.
// Depende só das interfaces de source, parser, linker e repository.
package extractor

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor/ir"
	"github.com/BMokarzel/rhaxis-code-two/extractor/linker"
	"github.com/BMokarzel/rhaxis-code-two/extractor/repository"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source"
)

type Parser interface {
	Extract(ctx context.Context, fileID string, f source.File) (*ir.FileGraph, error)
}

type Linker interface {
	Link(ctx context.Context, in linker.Input) (*entity.Graph, error)
}

// Enricher roda depois da linkagem e antes da persistência, mutando o grafo in-place.
// Usado para passos pós-link que precisam do grafo pronto (authorship, cross-service, ...).
type Enricher interface {
	Enrich(ctx context.Context, app entity.Application, g *entity.Graph) error
}

type Extractor struct {
	Parser     Parser
	Linker     Linker
	Repository repository.GraphRepository
	Enrichers  []Enricher
	Workers    int
}

type FileError struct {
	Path string
	Err  error
}

type Report struct {
	Files  int
	Failed []FileError
	Nodes  int
	Edges  int
}

func FileID(app entity.Application, path string) string {
	return app.Key + ":" + path
}

func (o *Extractor) Run(ctx context.Context, app entity.Application, src source.Provider) (*Report, error) {
	workers := o.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	type result struct {
		fg   *ir.FileGraph
		path string
		err  error
	}
	files := make(chan source.File)
	results := make(chan result)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range files {
				fg, err := o.Parser.Extract(ctx, FileID(app, f.Path), f)
				results <- result{fg: fg, path: f.Path, err: err}
			}
		}()
	}

	var walkErr error
	go func() {
		walkErr = src.Walk(ctx, func(f source.File) error {
			select {
			case files <- f:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		close(files)
		wg.Wait()
		close(results)
	}()

	report := &Report{}
	var graphs []*ir.FileGraph
	for r := range results {
		report.Files++
		if r.err != nil {
			report.Failed = append(report.Failed, FileError{Path: r.path, Err: r.err})
			continue
		}
		graphs = append(graphs, r.fg)
	}
	if walkErr != nil {
		return report, fmt.Errorf("lendo arquivos: %w", walkErr)
	}
	sort.Slice(graphs, func(i, j int) bool { return graphs[i].File.Path < graphs[j].File.Path })

	g, err := o.Linker.Link(ctx, linker.Input{App: app, Files: graphs, ReadFile: src.ReadFile})
	if err != nil {
		return report, fmt.Errorf("linkando: %w", err)
	}

	for _, e := range o.Enrichers {
		if err := e.Enrich(ctx, app, g); err != nil {
			return report, fmt.Errorf("enriquecendo: %w", err)
		}
	}

	report.Nodes, report.Edges = len(g.Nodes), len(g.Edges)

	if err := o.Repository.ReplaceApplication(ctx, app, g); err != nil {
		return report, fmt.Errorf("persistindo: %w", err)
	}
	return report, nil
}
