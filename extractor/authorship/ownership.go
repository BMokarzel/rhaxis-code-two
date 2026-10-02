package authorship

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BMokarzel/rhaxis-code-two/entity"
)

// isOwnableDeclaration decide se um node recebe OWNS/CREATED/LAST_MODIFIED.
// Mantém a decisão do plano (seção 2.2): só declarações, nunca Call/Assignment/If/etc.
func isOwnableDeclaration(n entity.Node) bool {
	switch n.Type() {
	case entity.FunctionNode, entity.ClassNode, entity.InterfaceNode,
		entity.FieldNode, entity.EnumNode, entity.EnumMemberNode:
		return true
	case entity.VariableNode:
		// Só variáveis "de topo": owner é o File, não uma função/bloco.
		if c, ok := n.(entity.Code); ok && strings.HasSuffix(c.Owner(), ".ts") ||
			ok && strings.HasSuffix(c.Owner(), ".js") ||
			ok && strings.HasSuffix(c.Owner(), ".tsx") ||
			ok && strings.HasSuffix(c.Owner(), ".jsx") {
			return true
		}
		return false
	}
	return false
}

type ownableDecl struct {
	id       string
	fileID   string
	startLn  int
	endLn    int
	span     int
}

// collectDecls agrupa declarações elegíveis por fileID, ordenadas por span
// ascendente — a primeira que contém a linha é a "mais interna".
func collectDecls(g *entity.Graph) map[string][]ownableDecl {
	byFile := map[string][]ownableDecl{}
	for _, n := range g.Nodes {
		if !isOwnableDeclaration(n) {
			continue
		}
		c, ok := n.(entity.Code)
		if !ok {
			continue
		}
		loc := c.Location()
		if loc.FileID == "" || loc.StartLine <= 0 || loc.EndLine < loc.StartLine {
			continue
		}
		span := loc.EndLine - loc.StartLine + 1
		byFile[loc.FileID] = append(byFile[loc.FileID], ownableDecl{
			id:      n.ID(),
			fileID:  loc.FileID,
			startLn: loc.StartLine,
			endLn:   loc.EndLine,
			span:    span,
		})
	}
	for k := range byFile {
		decls := byFile[k]
		sort.Slice(decls, func(i, j int) bool { return decls[i].span < decls[j].span })
		byFile[k] = decls
	}
	return byFile
}

// innermost devolve a declaração de menor span que contém a linha. Nil se nenhuma.
func innermost(decls []ownableDecl, line int) *ownableDecl {
	for i := range decls {
		if decls[i].startLn <= line && line <= decls[i].endLn {
			return &decls[i]
		}
	}
	return nil
}

type declStats struct {
	authors   map[string]int   // personID → linhas
	minTime   int64            // menor author-time
	maxTime   int64
	minPerson string
	maxPerson string
	total     int
}

// deriveOwnership roda blame em cada File do grafo e emite OWNS/CREATED/LAST_MODIFIED.
// Pessoas vistas apenas no blame (fora do range do git log) ganham Person node novo.
func deriveOwnership(ctx context.Context, g *entity.Graph, _ entity.Application, in Input, cfg Config) error {
	byFile := collectDecls(g)
	if len(byFile) == 0 {
		return nil
	}

	// Index de files por ID → path (relativo ao Subdir).
	filePathByID := map[string]string{}
	for _, n := range g.Nodes {
		if f, ok := n.(entity.File); ok {
			filePathByID[f.ID()] = f.Path
		}
	}

	// Dedup de Person já no grafo.
	personSeen := map[string]bool{}
	for _, n := range g.Nodes {
		if p, ok := n.(entity.Person); ok {
			personSeen[p.ID()] = true
		}
	}

	// Para cada file com declarações, rode blame uma vez.
	for fileID, decls := range byFile {
		relPath, ok := filePathByID[fileID]
		if !ok {
			continue
		}
		fullPath := relPath
		if in.Subdir != "" && in.Subdir != "." {
			fullPath = strings.TrimSuffix(filepath.ToSlash(in.Subdir), "/") + "/" + relPath
		}

		lines, err := runBlame(ctx, in.Repo, in.Rev, fullPath)
		if err != nil {
			// Arquivo pode estar untracked (novo, não commitado ainda) ou com
			// problema específico — segue a vida sem emitir OWNS pra esse file.
			fmt.Fprintf(os.Stderr, "authorship: blame %s: %v\n", fullPath, err)
			continue
		}

		// Acumula stats por declaração.
		stats := map[string]*declStats{}
		for _, bl := range lines {
			d := innermost(decls, bl.FinalLine)
			if d == nil {
				continue
			}
			person := toPerson(bl.Author, cfg.StoreEmail)
			if !personSeen[person.ID()] {
				g.Nodes = append(g.Nodes, person)
				personSeen[person.ID()] = true
			}
			s := stats[d.id]
			if s == nil {
				s = &declStats{authors: map[string]int{}, minTime: bl.Time, maxTime: bl.Time, minPerson: person.ID(), maxPerson: person.ID()}
				stats[d.id] = s
			}
			s.authors[person.ID()]++
			s.total++
			if bl.Time < s.minTime {
				s.minTime = bl.Time
				s.minPerson = person.ID()
			}
			if bl.Time > s.maxTime {
				s.maxTime = bl.Time
				s.maxPerson = person.ID()
			}
		}

		// Emite edges.
		for declID, s := range stats {
			if s.total == 0 {
				continue
			}
			// OWNS por autor, ordenado por nome para determinismo.
			ids := make([]string, 0, len(s.authors))
			for id := range s.authors {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, pid := range ids {
				share := float64(s.authors[pid]) / float64(s.total)
				g.Edges = append(g.Edges, entity.Edge{
					Type:  entity.OwnsEdge,
					From:  pid,
					To:    declID,
					Kind:  "blame",
					Share: share,
				})
			}
			g.Edges = append(g.Edges, entity.Edge{
				Type: entity.CreatedEdge,
				From: s.minPerson,
				To:   declID,
			})
			g.Edges = append(g.Edges, entity.Edge{
				Type: entity.LastModifiedEdge,
				From: s.maxPerson,
				To:   declID,
			})
		}
	}
	return nil
}
