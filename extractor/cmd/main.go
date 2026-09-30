// Binário: extrai o grafo de código de uma aplicação local.
//
// Sem NEO4J_URI setado: grava JSON via memory repository.
// Com NEO4J_URI setado: persiste no Neo4j.
//
//	go run ./extractor/cmd -path ./minha-api -key minha-api -out graph.json
//	NEO4J_URI=bolt://localhost:7687 go run ./extractor/cmd -path ./minha-api -key minha-api
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

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

func main() {
	root := flag.String("path", ".", "diretório raiz da aplicação")
	key := flag.String("key", "", "chave da aplicação (padrão: nome do diretório)")
	out := flag.String("out", "-", "arquivo de saída JSON (ignorado se NEO4J_URI setado; - para stdout)")
	flag.Parse()

	if err := run(*root, *key, *out); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run(root, key, out string) error {
	ctx := context.Background()
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if key == "" {
		key = filepath.Base(abs)
	}
	app := entity.Application{Base: entity.Base{NodeID: "app:" + key}, Name: key, Key: key}

	registry := parser.NewRegistry(javascript.New())
	js := jsresolver.NewFactory()

	uri := os.Getenv("NEO4J_URI")
	if uri != "" {
		return runNeo4j(ctx, uri, app, registry, js, abs)
	}
	return runMemory(ctx, app, registry, js, abs, out)
}

func runMemory(ctx context.Context, app entity.Application, registry *parser.Registry, js linker.ResolverFactory, abs, out string) error {
	repo := memory.New()
	o := &extractor.Extractor{
		Parser: registry,
		Linker: linker.New(map[string]linker.ResolverFactory{
			javascript.LangJavaScript: js,
			javascript.LangTypeScript: js,
			javascript.LangTSX:        js,
		}),
		Repository: repo,
	}

	report, err := o.Run(ctx, app, local.New(abs, registry.Extensions()))
	if err != nil {
		return err
	}
	printReport(report)

	w := os.Stdout
	if out != "-" {
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	return repo.WriteJSON(w, app.ID())
}

func runNeo4j(ctx context.Context, uri string, app entity.Application, registry *parser.Registry, js linker.ResolverFactory, abs string) error {
	user := getenv("NEO4J_USER", "neo4j")
	pass := getenv("NEO4J_PASSWORD", "neo4j")
	repo, err := neo4jrepo.New(ctx, uri, user, pass)
	if err != nil {
		return err
	}
	defer repo.Close(ctx)

	o := &extractor.Extractor{
		Parser: registry,
		Linker: linker.New(map[string]linker.ResolverFactory{
			javascript.LangJavaScript: js,
			javascript.LangTypeScript: js,
			javascript.LangTSX:        js,
		}),
		Repository: repo,
	}
	report, err := o.Run(ctx, app, local.New(abs, registry.Extensions()))
	if err != nil {
		return err
	}
	printReport(report)
	fmt.Fprintf(os.Stderr, "persistido em Neo4j (%s) app=%s\n", uri, app.Key)
	return nil
}

func printReport(report *extractor.Report) {
	for _, f := range report.Failed {
		fmt.Fprintf(os.Stderr, "falha em %s: %v\n", f.Path, f.Err)
	}
	fmt.Fprintf(os.Stderr, "%d arquivos, %d nodes, %d edges\n", report.Files, report.Nodes, report.Edges)
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
