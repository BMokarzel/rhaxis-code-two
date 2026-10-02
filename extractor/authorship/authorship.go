package authorship

import (
	"context"
	"fmt"
	"strings"

	"github.com/BMokarzel/rhaxis-code-two/entity"
)

// Input é a entrada do passo de enriquecimento. Repo, Rev e Subdir vêm do mesmo
// provider que source/git usa; Range/Limit vêm da config (ou flags).
type Input struct {
	Repo   string // caminho do repo (.git)
	Rev    string // SHA/branch/tag; vazio = HEAD
	Subdir string // monorepo
	Range  string // ex.: "main~50..HEAD"; vazio = usa Rev (todo histórico até rev)
	Limit  int    // 0 = sem limite
}

// Enrich adiciona Commit/Person e edges AUTHORED_BY/COMMITTED_BY/CHANGED ao
// grafo. É um no-op se cfg.Enabled for false. Os edges CHANGED só são emitidos
// para arquivos que já existem no grafo (fileIDs mapeados por path).
func Enrich(ctx context.Context, g *entity.Graph, app entity.Application, in Input, cfg Config) error {
	if !cfg.Enabled {
		return nil
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if in.Repo == "" {
		return fmt.Errorf("authorship: Repo é obrigatório")
	}
	if in.Rev == "" {
		in.Rev = "HEAD"
	}
	rangeExpr := in.Range
	if rangeExpr == "" {
		rangeExpr = in.Rev
	}

	commits, err := runGitLog(ctx, in.Repo, rangeExpr, in.Subdir, in.Limit)
	if err != nil {
		return err
	}

	// Índice de File por path. CHANGED só é emitida se o path está no grafo
	// atual — evita criar edges para arquivos que ficaram fora da extração
	// (binários, docs, lockfiles, extensões não suportadas).
	fileIDByPath := map[string]string{}
	for _, n := range g.Nodes {
		if f, ok := n.(entity.File); ok {
			fileIDByPath[f.Path] = f.ID()
		}
	}

	// Dedup de Person entre múltiplos commits.
	personSeen := map[string]bool{}
	// Dedup de Commit (defesa — git log não repete, mas enriquecimentos
	// futuros podem rodar isto duas vezes com ranges diferentes).
	commitSeen := map[string]bool{}

	for _, c := range commits {
		if commitSeen[c.SHA] {
			continue
		}
		commitSeen[c.SHA] = true

		commit := entity.Commit{
			Base:        entity.Base{NodeID: "commit:" + c.SHA},
			SHA:         c.SHA,
			Message:     c.Subject,
			AuthoredAt:  c.AuthoredAt,
			CommittedAt: c.CommittedAt,
			Parent:      c.ParentSHA,
		}
		g.Nodes = append(g.Nodes, commit)

		author := toPerson(c.Author, cfg.StoreEmail)
		if !personSeen[author.ID()] {
			g.Nodes = append(g.Nodes, author)
			personSeen[author.ID()] = true
		}
		g.Edges = append(g.Edges, entity.Edge{
			Type: entity.AuthoredByEdge, From: commit.ID(), To: author.ID(),
		})

		if c.Committer.Email != c.Author.Email {
			committer := toPerson(c.Committer, cfg.StoreEmail)
			if !personSeen[committer.ID()] {
				g.Nodes = append(g.Nodes, committer)
				personSeen[committer.ID()] = true
			}
			g.Edges = append(g.Edges, entity.Edge{
				Type: entity.CommittedByEdge, From: commit.ID(), To: committer.ID(),
			})
		}

		changed, err := runDiffTree(ctx, in.Repo, c.SHA, in.Subdir)
		if err != nil {
			return err
		}
		for _, cf := range changed {
			fid, ok := fileIDByPath[cf.Path]
			if !ok {
				// Pode ser (a) arquivo ignorado pelo extractor (extensão fora do registry,
				// node_modules, etc.) ou (b) arquivo deletado no commit — nesses casos não
				// temos node File para ligar. Silêncio é correto.
				continue
			}
			edge := entity.Edge{
				Type: entity.ChangedEdge,
				From: commit.ID(),
				To:   fid,
				Kind: statusToKind(cf.Status),
			}
			if cf.RenamedFrom != "" {
				edge.Member = cf.RenamedFrom // reusa o campo Member para "renamedFrom"
			}
			g.Edges = append(g.Edges, edge)
		}
	}

	// Blame-level ownership: OWNS/CREATED/LAST_MODIFIED por declaração.
	// Roda uma vez por file do grafo, após CHANGED, para reaproveitar o mesmo
	// Rev/Subdir. Erros aqui não devem derrubar o resto da autoria — já tratados
	// internamente (blame de arquivo untracked só vira log em stderr).
	if err := deriveOwnership(ctx, g, app, in, cfg); err != nil {
		return err
	}

	return nil
}

// Enricher adapta a função Enrich para a interface extractor.Enricher, que roda
// pós-link e pré-persistência. Mantém a configuração/input ao redor, para que o
// CLI só precise fornecer o repo e as flags uma vez.
type Enricher struct {
	Input  Input
	Config Config
}

func (e Enricher) Enrich(ctx context.Context, app entity.Application, g *entity.Graph) error {
	return Enrich(ctx, g, app, e.Input, e.Config)
}

// FriendlyRange devolve uma descrição humana do que vai ser percorrido, útil
// para o log final do CLI.
func FriendlyRange(in Input) string {
	if in.Range != "" {
		if in.Limit > 0 {
			return fmt.Sprintf("%s (limit %d)", in.Range, in.Limit)
		}
		return in.Range
	}
	parts := []string{"rev=" + in.Rev}
	if in.Limit > 0 {
		parts = append(parts, fmt.Sprintf("limit=%d", in.Limit))
	}
	return strings.Join(parts, " ")
}
