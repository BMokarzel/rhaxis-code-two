package crosservice

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"

	"github.com/BMokarzel/rhaxis-code-two/entity"
)

// LinkReport sumariza um run de Link.
type LinkReport struct {
	Exact     int
	Inferred  int
	Ambiguous int
	Unmatched int // sinais sem nenhum candidato — contados pra audit
	Edges     []entity.Edge
	Reviews   []entity.LinkReview
}

// Link roda R1 (URL) e R2 (env) para todas as apps do snapshot. Produz edges
// REQUESTS (App → App) e LinkReview para resoluções ambíguas. Edges antigas
// e reviews da mesma origem/tipo devem ser removidos pelo chamador antes de
// aplicar o novo conjunto — Link é idempotente mas não limpa o store.
func Link(apps []entity.Application) *LinkReport {
	rep := &LinkReport{}
	for _, src := range apps {
		linkOne(src, apps, rep)
	}
	return rep
}

func linkOne(src entity.Application, all []entity.Application, rep *LinkReport) {
	// R1 — URL
	for _, m := range resolveURL(src, all) {
		sortByScore(m.Candidates)
		emit(src, "url", m.Host, m.Candidates, rep)
	}
	// R2 — env
	for _, m := range resolveEnv(src, all) {
		sortByScore(m.Candidates)
		emit(src, "env", m.Env, m.Candidates, rep)
	}
}

// emit converte candidatos ranqueados em edges + reviews conforme a regra:
//
//	1 candidato        → REQUESTS exact/inferred (exact só se score top>=2)
//	2+ candidatos      → REQUESTS inferred para o top + LinkReview ambiguous
//	0 candidatos       → silêncio (incrementa Unmatched)
func emit(src entity.Application, kind, hint string, cands []scoredApp, rep *LinkReport) {
	if len(cands) == 0 {
		rep.Unmatched++
		return
	}
	top := cands[0]
	res := entity.ResolutionInferred
	if len(cands) == 1 && top.Score >= 2 {
		res = entity.ResolutionExact
	}
	if len(cands) > 1 {
		// Se há empate forte no topo (score igual ao segundo), marca ambíguo.
		if cands[1].Score == top.Score {
			res = entity.ResolutionAmbiguous
		}
	}

	switch res {
	case entity.ResolutionExact:
		rep.Exact++
	case entity.ResolutionAmbiguous:
		rep.Ambiguous++
	default:
		rep.Inferred++
	}

	rep.Edges = append(rep.Edges, entity.Edge{
		Type:       entity.RequestsEdge,
		From:       src.ID(),
		To:         top.App.ID(),
		Kind:       kind,
		Member:     hint,
		Resolution: res,
	})

	if res == entity.ResolutionAmbiguous {
		rep.Reviews = append(rep.Reviews, newReview(src, kind, hint, top.App.ID(), cands))
	}
}

// newReview monta um LinkReview com ID determinístico (sha1 de source+kind+hint),
// portanto reaplicável sem duplicar.
func newReview(src entity.Application, kind, hint, chosen string, cands []scoredApp) entity.LinkReview {
	h := sha1.Sum([]byte(src.ID() + "|" + kind + "|" + hint))
	id := src.Key + ":review:" + hex.EncodeToString(h[:])[:12]

	targets := make([]string, 0, len(cands))
	scores := make([]int, 0, len(cands))
	whys := make([]string, 0, len(cands))
	for _, c := range cands {
		targets = append(targets, c.App.ID())
		scores = append(scores, c.Score)
		whys = append(whys, c.Why)
	}
	return entity.LinkReview{
		Base:             entity.Base{NodeID: id},
		CallID:           src.ID(), // granularidade de app por enquanto; Call ID vem no refinamento
		EdgeType:         entity.RequestsEdge,
		ChosenID:         chosen,
		CandidateTargets: targets,
		CandidateScores:  scores,
		CandidateWhys:    whys,
		Hint:             kind + ":" + hint,
		Status:           "pending",
	}
}

func sortByScore(c []scoredApp) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].Score != c[j].Score {
			return c[i].Score > c[j].Score
		}
		// desempate por Key para ordem estável
		return c[i].App.Key < c[j].App.Key
	})
}
