// Package crosservice liga diferentes Applications do grafo via URL/env/tópico.
//
// signals.go denormaliza na Application 3 sinais já observáveis no grafo:
//   - Endpoints: coletados dos nodes `Endpoint` do próprio app
//   - EnvVarsRead: coletados das edges `READS Function → External{process}` cuja
//     Member começa com "env." — o resto do Member é o nome da env var
//   - HostCandidates: coletados por regex sobre o conteúdo dos arquivos; usa
//     source.Provider porque literais de string não são nodes hoje
//
// Essa denormalização existe para que o linker cross-service (R1/R2) consulte
// candidatos em O(1) ao invés de varrer o grafo inteiro de outra app. Limites
// de corte estão em consts para não deixar a Application inchar.
package crosservice

import (
	"context"
	"regexp"
	"sort"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source"
)

const (
	maxEndpoints = 50
	maxEnvVars   = 20
	maxHosts     = 20
)

// SignalsEnricher implementa extractor.Enricher: popula Application.Endpoints,
// EnvVarsRead e HostCandidates antes da persistência. Src é opcional — se nil,
// HostCandidates fica vazio (útil em testes).
type SignalsEnricher struct {
	Src source.Provider
}

func (e SignalsEnricher) Enrich(ctx context.Context, app entity.Application, g *entity.Graph) error {
	endpoints := collectEndpointSignals(g)
	envVars := collectEnvVarSignals(g)
	var hosts []string
	if e.Src != nil {
		hs, err := collectHostSignals(ctx, e.Src)
		if err != nil {
			return err
		}
		hosts = hs
	}
	attachSignalsToApp(g, app, endpoints, envVars, hosts)
	return nil
}

// collectEndpointSignals formata "METHOD PATH" para cada Endpoint do grafo.
// Ordena alfabeticamente para determinismo entre runs; corta no limite.
func collectEndpointSignals(g *entity.Graph) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range g.Nodes {
		ep, ok := n.(entity.Endpoint)
		if !ok {
			continue
		}
		key := ep.Method + " " + ep.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	sort.Strings(out)
	if len(out) > maxEndpoints {
		out = out[:maxEndpoints]
	}
	return out
}

// collectEnvVarSignals olha para READS que terminam no External{process} com
// Member "env.<NAME>" (ou "env.<NAME>.<...>" — pega só o primeiro componente).
func collectEnvVarSignals(g *entity.Graph) []string {
	// Index: ID → Node para resolver o target do READS.
	byID := map[string]entity.Node{}
	for _, n := range g.Nodes {
		byID[n.ID()] = n
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range g.Edges {
		if e.Type != entity.ReadsEdge {
			continue
		}
		if e.Member == "" {
			continue
		}
		target, ok := byID[e.To]
		if !ok {
			continue
		}
		ext, ok := target.(entity.External)
		if !ok || ext.Name != "process" {
			continue
		}
		name := envVarFromMember(e.Member)
		if name == "" {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	if len(out) > maxEnvVars {
		out = out[:maxEnvVars]
	}
	return out
}

// envVarFromMember extrai o nome da env var de uma string como "env.FOO" ou
// "env.FOO.bar" (devolve "FOO"). Devolve "" se não começa com "env.".
func envVarFromMember(member string) string {
	const prefix = "env."
	if len(member) <= len(prefix) || member[:len(prefix)] != prefix {
		return ""
	}
	rest := member[len(prefix):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '.' {
			return rest[:i]
		}
	}
	return rest
}

// urlLiteralRE casa URLs embutidas em strings de código: aspas, backticks,
// com ou sem porta. Prefixo opcional por protocolo; se omitido, exige dois
// componentes separados por `:` para parecer host:port (reduz falso positivo).
var urlLiteralRE = regexp.MustCompile(`(?i)(?:https?|ws|wss)://([a-z0-9][a-z0-9.\-]*(?::\d{2,5})?)`)

// collectHostSignals walka os arquivos do source.Provider e extrai hosts de
// literais de URL. É resiliente: erros de leitura de arquivos individuais são
// ignorados (podem ser arquivos binários ou removidos entre Walk e ReadFile).
func collectHostSignals(ctx context.Context, src source.Provider) ([]string, error) {
	seen := map[string]bool{}
	err := src.Walk(ctx, func(f source.File) error {
		data, err := src.ReadFile(ctx, f.Path)
		if err != nil {
			return nil
		}
		for _, m := range urlLiteralRE.FindAllSubmatch(data, -1) {
			if len(m) < 2 {
				continue
			}
			seen[string(m[1])] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	if len(out) > maxHosts {
		out = out[:maxHosts]
	}
	return out, nil
}

// attachSignalsToApp localiza a Application no grafo e substitui com cópia
// contendo os sinais. Também atualiza o `app` original se ele é passado como
// value — o chamador precisa ler de g.Nodes para pegar a versão enriquecida.
func attachSignalsToApp(g *entity.Graph, app entity.Application, endpoints, envVars, hosts []string) {
	for i, n := range g.Nodes {
		a, ok := n.(entity.Application)
		if !ok || a.ID() != app.ID() {
			continue
		}
		a.Endpoints = endpoints
		a.EnvVarsRead = envVars
		a.HostCandidates = hosts
		g.Nodes[i] = a
		return
	}
	// Application ainda não está no grafo — raro, mas acontece se o linker
	// não a adicionou. Adiciona agora.
	app.Endpoints = endpoints
	app.EnvVarsRead = envVars
	app.HostCandidates = hosts
	g.Nodes = append(g.Nodes, app)
}
