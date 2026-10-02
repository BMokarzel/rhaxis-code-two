// Binário: extrai o grafo de código de uma aplicação local.
//
// Sem subcomando (ou "extract"): extrai uma app.
//   go run ./extractor/cmd -path ./minha-api -key minha-api -out graph.json
//   go run ./extractor/cmd -path . -rev HEAD -key app
//   NEO4J_URI=bolt://localhost:7687 go run ./extractor/cmd -path . -key app
//
// Subcomando "link-services": liga apps já persistidas no Neo4j.
//   NEO4J_URI=... go run ./extractor/cmd link-services
//   NEO4J_URI=... go run ./extractor/cmd link-services review
//   NEO4J_URI=... go run ./extractor/cmd link-services confirm <reviewID>
//   NEO4J_URI=... go run ./extractor/cmd link-services override <reviewID> <targetID>
//   NEO4J_URI=... go run ./extractor/cmd link-services reject <reviewID>
//   NEO4J_URI=... go run ./extractor/cmd link-services audit
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor"
	"github.com/BMokarzel/rhaxis-code-two/extractor/authorship"
	"github.com/BMokarzel/rhaxis-code-two/extractor/crosservice"
	"github.com/BMokarzel/rhaxis-code-two/extractor/linker"
	jsresolver "github.com/BMokarzel/rhaxis-code-two/extractor/linker/javascript"
	"github.com/BMokarzel/rhaxis-code-two/extractor/parser"
	"github.com/BMokarzel/rhaxis-code-two/extractor/parser/javascript"
	"github.com/BMokarzel/rhaxis-code-two/extractor/repository/memory"
	neo4jrepo "github.com/BMokarzel/rhaxis-code-two/extractor/repository/neo4j"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source"
	gitsrc "github.com/BMokarzel/rhaxis-code-two/extractor/source/git"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source/local"
)

type cliFlags struct {
	root          string
	key           string
	out           string
	rev           string
	subdir        string
	config        string
	authorshipOn  bool
	authorshipOff bool
	storeEmail    string
	commitsRange  string
	commitsLimit  int
}

func main() {
	// Despacho de subcomando: primeiro arg (que não começa com `-`) é o comando.
	// Padrão: "extract" para preservar o uso anterior sem subcomando.
	args := os.Args[1:]
	cmd := "extract"
	if len(args) > 0 && !isFlag(args[0]) {
		cmd = args[0]
		args = args[1:]
	}

	var err error
	switch cmd {
	case "extract":
		err = runExtract(args)
	case "link-services":
		err = runLinkServices(args)
	default:
		fmt.Fprintln(os.Stderr, "comando desconhecido:", cmd)
		fmt.Fprintln(os.Stderr, "comandos: extract, link-services")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func isFlag(s string) bool {
	return len(s) > 0 && s[0] == '-'
}

func runExtract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	var f cliFlags
	fs.StringVar(&f.root, "path", ".", "diretório raiz da aplicação (ou repo git quando -rev é usado)")
	fs.StringVar(&f.key, "key", "", "chave da aplicação (padrão: nome do diretório ou do subdir)")
	fs.StringVar(&f.out, "out", "-", "arquivo de saída JSON (ignorado se NEO4J_URI setado; - para stdout)")
	fs.StringVar(&f.rev, "rev", "", "revisão git (SHA, branch, tag); sem valor usa o filesystem local")
	fs.StringVar(&f.subdir, "subdir", "", "subdiretório do monorepo a extrair (relativo ao repo); só com -rev")
	fs.StringVar(&f.config, "config", ".rhaxis.yaml", "arquivo de configuração; não falha se ausente")
	fs.BoolVar(&f.authorshipOn, "authorship", false, "liga authorship (sobrescreve .rhaxis.yaml)")
	fs.BoolVar(&f.authorshipOff, "no-authorship", false, "desliga authorship (sobrescreve .rhaxis.yaml)")
	fs.StringVar(&f.storeEmail, "store-email", "", "plain|hash|none (sobrescreve .rhaxis.yaml)")
	fs.StringVar(&f.commitsRange, "commits-range", "", "expressão git para o git log (ex.: 'main~50..HEAD')")
	fs.IntVar(&f.commitsLimit, "commits-limit", 0, "número máximo de commits lidos (0 = sem limite)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runExtractWith(f)
}

func runExtractWith(f cliFlags) error {
	ctx := context.Background()
	abs, err := filepath.Abs(f.root)
	if err != nil {
		return err
	}
	key := f.key
	if key == "" {
		if f.subdir != "" && f.subdir != "." {
			key = filepath.Base(f.subdir)
		} else {
			key = filepath.Base(abs)
		}
	}
	app := entity.Application{Base: entity.Base{NodeID: "app:" + key}, Name: key, Key: key}

	registry := parser.NewRegistry(javascript.New())
	js := jsresolver.NewFactory()

	src := buildSource(abs, f.rev, f.subdir, registry.Extensions())

	authCfg, err := resolveAuthorship(abs, f)
	if err != nil {
		return err
	}
	enrichers := buildEnrichers(abs, f, authCfg, src)

	uri := os.Getenv("NEO4J_URI")
	if uri != "" {
		return runNeo4j(ctx, uri, app, registry, js, src, enrichers)
	}
	return runMemory(ctx, app, registry, js, src, enrichers, f.out)
}

func buildSource(abs, rev, subdir string, exts map[string]string) source.Provider {
	if rev != "" {
		return gitsrc.New(abs, rev, subdir, exts)
	}
	return local.New(abs, exts)
}

// resolveAuthorship combina .rhaxis.yaml (se existir) com flags de CLI (que
// têm precedência). Devolve a Config final já com defaults aplicados.
func resolveAuthorship(abs string, f cliFlags) (authorship.Config, error) {
	cfg, err := authorship.LoadFile(filepath.Join(abs, f.config))
	if err != nil {
		return authorship.Config{}, err
	}
	if f.authorshipOn {
		cfg.Enabled = true
	}
	if f.authorshipOff {
		cfg.Enabled = false
	}
	if f.storeEmail != "" {
		cfg.StoreEmail = authorship.StoreEmail(f.storeEmail)
	}
	if f.commitsRange != "" {
		cfg.CommitsRange = f.commitsRange
	}
	if f.commitsLimit > 0 {
		cfg.CommitsLimit = f.commitsLimit
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return authorship.Config{}, err
	}
	return cfg, nil
}

func buildEnrichers(abs string, f cliFlags, cfg authorship.Config, src source.Provider) []extractor.Enricher {
	var enrichers []extractor.Enricher
	// signals roda sempre: denormaliza Endpoints/EnvVars/HostCandidates na
	// Application; é no-op se o grafo não tem nenhum desses sinais.
	enrichers = append(enrichers, crosservice.SignalsEnricher{Src: src})
	// topics varre o código em busca de .publish/.subscribe/.sendMessage etc.
	// e emite nodes Topic + PRODUCES/CONSUMES — base para R3 cross-service.
	enrichers = append(enrichers, crosservice.TopicsEnricher{Src: src})
	if cfg.Enabled {
		if f.rev == "" {
			fmt.Fprintln(os.Stderr, "aviso: --authorship sem -rev; usando HEAD do repo em", abs)
		}
		enrichers = append(enrichers, authorship.Enricher{
			Input: authorship.Input{
				Repo:   abs,
				Rev:    f.rev,
				Subdir: f.subdir,
				Range:  cfg.CommitsRange,
				Limit:  cfg.CommitsLimit,
			},
			Config: cfg,
		})
	}
	return enrichers
}

func runMemory(ctx context.Context, app entity.Application, registry *parser.Registry, js linker.ResolverFactory, src source.Provider, enrichers []extractor.Enricher, out string) error {
	repo := memory.New()
	o := &extractor.Extractor{
		Parser: registry,
		Linker: linker.New(map[string]linker.ResolverFactory{
			javascript.LangJavaScript: js,
			javascript.LangTypeScript: js,
			javascript.LangTSX:        js,
		}),
		Repository: repo,
		Enrichers:  enrichers,
	}

	report, err := o.Run(ctx, app, src)
	if err != nil {
		return err
	}
	printReport(report)

	w := os.Stdout
	if out != "-" {
		file, err := os.Create(out)
		if err != nil {
			return err
		}
		defer file.Close()
		w = file
	}
	return repo.WriteJSON(w, app.ID())
}

func runNeo4j(ctx context.Context, uri string, app entity.Application, registry *parser.Registry, js linker.ResolverFactory, src source.Provider, enrichers []extractor.Enricher) error {
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
		Enrichers:  enrichers,
	}
	report, err := o.Run(ctx, app, src)
	if err != nil {
		return err
	}
	printReport(report)
	fmt.Fprintf(os.Stderr, "persistido em Neo4j (%s) app=%s\n", uri, app.Key)
	return nil
}

// runLinkServices despacha os sub-sub-comandos de link-services.
func runLinkServices(args []string) error {
	sub := ""
	if len(args) > 0 && !isFlag(args[0]) {
		sub = args[0]
		args = args[1:]
	}
	ctx := context.Background()
	uri := os.Getenv("NEO4J_URI")
	if uri == "" {
		return fmt.Errorf("link-services precisa de NEO4J_URI setado")
	}
	repo, err := neo4jrepo.New(ctx, uri, getenv("NEO4J_USER", "neo4j"), getenv("NEO4J_PASSWORD", "neo4j"))
	if err != nil {
		return err
	}
	defer repo.Close(ctx)

	switch sub {
	case "", "link":
		return linkServicesLink(ctx, repo)
	case "review":
		return linkServicesReview(ctx, repo)
	case "confirm":
		if len(args) != 1 {
			return fmt.Errorf("uso: link-services confirm <reviewID>")
		}
		return repo.SetReviewStatus(ctx, args[0], "confirmed")
	case "override":
		if len(args) != 2 {
			return fmt.Errorf("uso: link-services override <reviewID> <targetID>")
		}
		return linkServicesOverride(ctx, repo, args[0], args[1])
	case "reject":
		if len(args) != 1 {
			return fmt.Errorf("uso: link-services reject <reviewID>")
		}
		return linkServicesReject(ctx, repo, args[0])
	case "audit":
		return linkServicesAudit(ctx, repo)
	default:
		return fmt.Errorf("subcomando desconhecido: %s", sub)
	}
}

func linkServicesLink(ctx context.Context, repo *neo4jrepo.Repository) error {
	apps, err := repo.LoadApplications(ctx)
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		fmt.Fprintln(os.Stderr, "nenhuma application no banco; rode extract primeiro")
		return nil
	}
	rep := crosservice.Link(apps)
	// agrupa edges por app de origem para reaplicar via ReplaceCrossServiceLinks
	byApp := map[string][]entity.Edge{}
	reviewsByApp := map[string][]entity.LinkReview{}
	for _, e := range rep.Edges {
		byApp[e.From] = append(byApp[e.From], e)
	}
	for _, r := range rep.Reviews {
		// LinkReview.ID() = "<appKey>:review:...". Recupera appID.
		for _, a := range apps {
			if len(r.ID()) > len(a.Key)+1 && r.ID()[:len(a.Key)+1] == a.Key+":" {
				reviewsByApp[a.ID()] = append(reviewsByApp[a.ID()], r)
				break
			}
		}
	}
	for _, a := range apps {
		if err := repo.ReplaceCrossServiceLinks(ctx, a.ID(), byApp[a.ID()], reviewsByApp[a.ID()]); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "link-services: %d exact, %d inferred, %d ambiguous, %d unmatched\n",
		rep.Exact, rep.Inferred, rep.Ambiguous, rep.Unmatched)
	if rep.Ambiguous > 0 {
		fmt.Fprintln(os.Stderr, "use `link-services review` para listar ambiguidades")
	}
	return nil
}

func linkServicesReview(ctx context.Context, repo *neo4jrepo.Repository) error {
	reviews, err := repo.ListPendingReviews(ctx)
	if err != nil {
		return err
	}
	if len(reviews) == 0 {
		fmt.Println("nenhum review pendente")
		return nil
	}
	for _, r := range reviews {
		fmt.Printf("%s\n  hint:    %s\n  chosen:  %s\n  candidates:\n", r.ID(), r.Hint, r.ChosenID)
		for i, t := range r.CandidateTargets {
			score := 0
			why := ""
			if i < len(r.CandidateScores) {
				score = r.CandidateScores[i]
			}
			if i < len(r.CandidateWhys) {
				why = r.CandidateWhys[i]
			}
			fmt.Printf("    - %s (score=%d) %s\n", t, score, why)
		}
	}
	return nil
}

func linkServicesOverride(ctx context.Context, repo *neo4jrepo.Repository, reviewID, newTargetID string) error {
	// Carrega review para saber de qual app e qual chosen trocar.
	reviews, err := repo.ListPendingReviews(ctx)
	if err != nil {
		return err
	}
	var rv *entity.LinkReview
	for i := range reviews {
		if reviews[i].ID() == reviewID {
			rv = &reviews[i]
			break
		}
	}
	if rv == nil {
		return fmt.Errorf("review %s não encontrado (talvez já tenha sido resolvido)", reviewID)
	}
	// CallID em v1 é o appID. Troca a edge e marca review.
	if err := repo.UpdateRequestsTarget(ctx, rv.CallID, rv.ChosenID, newTargetID); err != nil {
		return err
	}
	return repo.SetReviewStatus(ctx, reviewID, "overridden")
}

func linkServicesReject(ctx context.Context, repo *neo4jrepo.Repository, reviewID string) error {
	reviews, err := repo.ListPendingReviews(ctx)
	if err != nil {
		return err
	}
	var rv *entity.LinkReview
	for i := range reviews {
		if reviews[i].ID() == reviewID {
			rv = &reviews[i]
			break
		}
	}
	if rv == nil {
		return fmt.Errorf("review %s não encontrado", reviewID)
	}
	if err := repo.DeleteRequestsEdge(ctx, rv.CallID, rv.ChosenID); err != nil {
		return err
	}
	return repo.SetReviewStatus(ctx, reviewID, "rejected")
}

func linkServicesAudit(ctx context.Context, repo *neo4jrepo.Repository) error {
	items, err := repo.AuditUnlinkedExternals(ctx)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("nenhuma chamada HTTP sem link")
		return nil
	}
	for _, it := range items {
		fmt.Printf("%s  %s:%d  %s\n", it.AppID, it.FileID, it.StartLine, it.CalleeText)
	}
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
