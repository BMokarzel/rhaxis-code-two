// rhaxis extrai o grafo de código de uma aplicação local e grava em JSON.
//
//	rhaxis -path ./minha-api -key minha-api -out graph.json
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor"
	"github.com/BMokarzel/rhaxis-code-two/extractor/javascript"
	"github.com/BMokarzel/rhaxis-code-two/linker"
	jsresolver "github.com/BMokarzel/rhaxis-code-two/linker/javascript"
	"github.com/BMokarzel/rhaxis-code-two/orchestrator"
	"github.com/BMokarzel/rhaxis-code-two/repository/memory"
	"github.com/BMokarzel/rhaxis-code-two/source/local"
)

func main() {
	root := flag.String("path", ".", "diretório raiz da aplicação")
	key := flag.String("key", "", "chave da aplicação (padrão: nome do diretório)")
	out := flag.String("out", "-", "arquivo de saída JSON (- para stdout)")
	flag.Parse()

	if err := run(*root, *key, *out); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run(root, key, out string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if key == "" {
		key = filepath.Base(abs)
	}
	app := entity.Application{Base: entity.Base{NodeID: "app:" + key}, Name: key, Key: key}

	registry := extractor.NewRegistry(javascript.New())
	js := jsresolver.NewFactory()
	repo := memory.New()
	o := &orchestrator.Orchestrator{
		Extractor: registry,
		Linker: linker.New(map[string]linker.ResolverFactory{
			javascript.LangJavaScript: js,
			javascript.LangTypeScript: js,
			javascript.LangTSX:        js,
		}),
		Repository: repo,
	}

	report, err := o.Run(context.Background(), app, local.New(abs, registry.Extensions()))
	if err != nil {
		return err
	}
	for _, f := range report.Failed {
		fmt.Fprintf(os.Stderr, "falha em %s: %v\n", f.Path, f.Err)
	}
	fmt.Fprintf(os.Stderr, "%d arquivos, %d nodes, %d edges\n", report.Files, report.Nodes, report.Edges)

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
