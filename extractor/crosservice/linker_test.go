package crosservice

import (
	"testing"

	"github.com/BMokarzel/rhaxis-code-two/entity"
)

func appWith(key string, hosts, envs []string) entity.Application {
	return entity.Application{
		Base:           entity.Base{NodeID: "app:" + key},
		Name:           key,
		Key:            key,
		HostCandidates: hosts,
		EnvVarsRead:    envs,
	}
}

func TestLinkURLExactByName(t *testing.T) {
	// users-api cita host "users:8080"; users (app) existe no snapshot.
	caller := appWith("users-api", []string{"users:8080"}, nil)
	target := appWith("users", nil, nil)

	rep := Link([]entity.Application{caller, target})

	if len(rep.Edges) != 1 {
		t.Fatalf("edges: %d, esperado 1 (%+v)", len(rep.Edges), rep.Edges)
	}
	e := rep.Edges[0]
	if e.Type != entity.RequestsEdge || e.From != caller.ID() || e.To != target.ID() {
		t.Errorf("edge errada: %+v", e)
	}
	if e.Kind != "url" || e.Member != "users:8080" {
		t.Errorf("edge metadata: kind=%q member=%q", e.Kind, e.Member)
	}
	if e.Resolution != entity.ResolutionExact {
		t.Errorf("resolution: %q, esperado exact", e.Resolution)
	}
	if rep.Exact != 1 || rep.Ambiguous != 0 {
		t.Errorf("counters: exact=%d ambiguous=%d", rep.Exact, rep.Ambiguous)
	}
}

func TestLinkURLAmbiguousGeneratesReview(t *testing.T) {
	caller := appWith("gateway", []string{"users:8080"}, nil)
	a := appWith("users", nil, nil)
	b := appWith("users-admin", nil, nil)

	rep := Link([]entity.Application{caller, a, b})

	// "users" casa com nameMatch 2 (prefix "users-admin" tem prefixo users- também)
	// Esperamos 1 edge (para o top) + 1 review
	var reqs []entity.Edge
	for _, e := range rep.Edges {
		if e.Type == entity.RequestsEdge {
			reqs = append(reqs, e)
		}
	}
	if len(reqs) != 1 {
		t.Fatalf("REQUESTS: %d, esperado 1", len(reqs))
	}
	if reqs[0].Resolution != entity.ResolutionAmbiguous {
		t.Errorf("resolution = %q, esperado ambiguous", reqs[0].Resolution)
	}
	if len(rep.Reviews) != 1 {
		t.Fatalf("Reviews: %d, esperado 1", len(rep.Reviews))
	}
	rev := rep.Reviews[0]
	if rev.Status != "pending" {
		t.Errorf("status: %q", rev.Status)
	}
	if len(rev.CandidateTargets) != 2 {
		t.Errorf("CandidateTargets: %v", rev.CandidateTargets)
	}
	if len(rev.CandidateScores) != 2 || len(rev.CandidateWhys) != 2 {
		t.Error("CandidateScores/Whys inconsistentes")
	}
	if rev.ChosenID == "" {
		t.Error("ChosenID vazio")
	}
}

func TestLinkURLNoMatchIsSilent(t *testing.T) {
	caller := appWith("x", []string{"stripe.com"}, nil)
	other := appWith("users", nil, nil)
	rep := Link([]entity.Application{caller, other})
	if len(rep.Edges) != 0 {
		t.Errorf("edges: %d, esperado 0", len(rep.Edges))
	}
	if len(rep.Reviews) != 0 {
		t.Errorf("reviews: %d, esperado 0", len(rep.Reviews))
	}
	if rep.Unmatched != 1 {
		t.Errorf("Unmatched: %d, esperado 1", rep.Unmatched)
	}
}

func TestLinkEnvInferredByPrefix(t *testing.T) {
	caller := appWith("gateway", nil, []string{"USERS_URL"})
	target := appWith("users", nil, nil)
	rep := Link([]entity.Application{caller, target})
	if len(rep.Edges) != 1 {
		t.Fatalf("edges: %d, esperado 1", len(rep.Edges))
	}
	e := rep.Edges[0]
	if e.Kind != "env" || e.Member != "USERS_URL" {
		t.Errorf("edge metadata: %+v", e)
	}
	// score=1 (só prefix) → inferred
	if e.Resolution != entity.ResolutionInferred {
		t.Errorf("resolution: %q, esperado inferred", e.Resolution)
	}
}

func TestLinkIgnoresSelf(t *testing.T) {
	// App que cita o próprio host não deve virar REQUESTS consigo mesma.
	me := appWith("users", []string{"users:8080"}, nil)
	rep := Link([]entity.Application{me})
	if len(rep.Edges) != 0 {
		t.Errorf("edges: %d, esperado 0 (self-link)", len(rep.Edges))
	}
}

func TestLinkDeterministicReviewID(t *testing.T) {
	// Rodar duas vezes com o mesmo input deve gerar IDs iguais.
	caller := appWith("gateway", []string{"users:8080"}, nil)
	a := appWith("users", nil, nil)
	b := appWith("users-admin", nil, nil)

	r1 := Link([]entity.Application{caller, a, b})
	r2 := Link([]entity.Application{caller, a, b})
	if len(r1.Reviews) != 1 || len(r2.Reviews) != 1 {
		t.Fatal("Reviews count inesperado")
	}
	if r1.Reviews[0].ID() != r2.Reviews[0].ID() {
		t.Errorf("IDs diferentes: %q vs %q", r1.Reviews[0].ID(), r2.Reviews[0].ID())
	}
}

func TestEnvEmptyPrefixNoMatch(t *testing.T) {
	caller := appWith("x", nil, []string{"UNRELATED"})
	target := appWith("other", nil, nil)
	rep := Link([]entity.Application{caller, target})
	// UNRELATED não casa com "other"
	if len(rep.Edges) != 0 {
		t.Errorf("edges: %d, esperado 0", len(rep.Edges))
	}
}
