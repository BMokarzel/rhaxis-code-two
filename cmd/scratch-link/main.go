// Scratch demo: carrega duas Applications de JSON e roda crosservice.Link
// sem Neo4j. Serve pra validar o pipeline cross-service em testes manuais.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor/crosservice"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "uso: scratch-link <app1.json> <app2.json> [...]")
		os.Exit(2)
	}
	var apps []entity.Application
	for _, p := range os.Args[1:] {
		a, err := loadApp(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, "falha carregando", p, err)
			os.Exit(1)
		}
		apps = append(apps, a)
		fmt.Printf("loaded %s (key=%s) endpoints=%v env=%v hosts=%v\n",
			a.ID(), a.Key, a.Endpoints, a.EnvVarsRead, a.HostCandidates)
	}
	rep := crosservice.Link(apps)
	fmt.Printf("\n=== LinkReport ===\n")
	fmt.Printf("exact=%d inferred=%d ambiguous=%d unmatched=%d\n",
		rep.Exact, rep.Inferred, rep.Ambiguous, rep.Unmatched)
	fmt.Printf("\n--- Edges (%d) ---\n", len(rep.Edges))
	for _, e := range rep.Edges {
		fmt.Printf("  %s --[%s/%s]--> %s\n", e.From, e.Type, e.Resolution, e.To)
	}
	fmt.Printf("\n--- Reviews (%d) ---\n", len(rep.Reviews))
	for _, r := range rep.Reviews {
		fmt.Printf("  %s  edge=%s chosen=%s hint=%q\n", r.ID(), r.EdgeType, r.ChosenID, r.Hint)
		for i, t := range r.CandidateTargets {
			fmt.Printf("    cand %s score=%d why=%s\n", t, r.CandidateScores[i], r.CandidateWhys[i])
		}
	}
}

func loadApp(path string) (entity.Application, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return entity.Application{}, err
	}
	var raw struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return entity.Application{}, err
	}
	for _, n := range raw.Nodes {
		if n["type"] != "Application" {
			continue
		}
		app := entity.Application{
			Base: entity.Base{NodeID: str(n["id"])},
			Name: str(n["name"]),
			Key:  str(n["key"]),
		}
		app.Endpoints = strs(n["endpoints"])
		app.EnvVarsRead = strs(n["envVarsRead"])
		app.HostCandidates = strs(n["hostCandidates"])
		return app, nil
	}
	return entity.Application{}, fmt.Errorf("sem Application em %s", path)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
func strs(v any) []string {
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
