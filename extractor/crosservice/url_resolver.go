package crosservice

import (
	"strings"

	"github.com/BMokarzel/rhaxis-code-two/entity"
)

// resolveURL faz R1 do plano: para cada host literal que a app `src` conhece,
// procura entre as outras apps `targets` quem casa. Devolve candidatos ranqueados
// por score (host match + path match). Resolver nunca prompta — se tem múltiplos
// candidatos, o chamador abre um LinkReview.
//
// Score:
//
//	+2 — alvo tem HostCandidates contendo o host, OU hostHead (parte antes de ":")
//	     casa com target.Key ou target.Name (match por nome do serviço)
//	+1 — alvo tem Endpoint cujo path casa com algum caminho que acompanha o host
//	     (não implementado nessa versão — hosts coletados pelo signals não trazem
//	     o path separado; fica como refinamento futuro quando CallSites forem
//	     emitidos pela fase de signals)
func resolveURL(src entity.Application, targets []entity.Application) []urlMatch {
	var out []urlMatch
	for _, host := range src.HostCandidates {
		hostHead := hostPart(host)
		var hits []scoredApp
		for _, t := range targets {
			if t.ID() == src.ID() {
				continue
			}
			score := scoreHost(host, hostHead, t)
			if score > 0 {
				hits = append(hits, scoredApp{App: t, Score: score, Why: whyHost(host, hostHead, t)})
			}
		}
		// Emite inclusive quando hits está vazio — o chamador conta como Unmatched.
		out = append(out, urlMatch{Host: host, Candidates: hits})
	}
	return out
}

// urlMatch agrupa um host literal da `src` com todos os alvos que o resolvem.
type urlMatch struct {
	Host       string
	Candidates []scoredApp // ordenados pelo chamador (sortByScore)
}

type scoredApp struct {
	App   entity.Application
	Score int
	Why   string
}

// hostPart devolve a parte do host antes do `:` (porta). Ex.: "users:8080" → "users".
func hostPart(h string) string {
	if i := strings.IndexByte(h, ':'); i > 0 {
		return h[:i]
	}
	return h
}

func scoreHost(host, hostHead string, t entity.Application) int {
	s := 0
	// host exato na lista de HostCandidates do alvo = sinal muito forte
	for _, h := range t.HostCandidates {
		if h == host {
			s += 2
			break
		}
	}
	if s > 0 {
		return s
	}
	// prefix match contra Key/Name do alvo
	if hostHead == "" {
		return 0
	}
	if nameMatch(hostHead, t.Key) || nameMatch(hostHead, t.Name) {
		s += 2
	}
	return s
}

// nameMatch considera equivalências brandas: igual, prefixo com `-`/`_`, ou
// hyphen/underscore normalizados.
func nameMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	na := normalizeName(a)
	nb := normalizeName(b)
	if na == nb {
		return true
	}
	if strings.HasPrefix(nb, na+"-") || strings.HasPrefix(nb, na+"_") {
		return true
	}
	if strings.HasPrefix(na, nb+"-") || strings.HasPrefix(na, nb+"_") {
		return true
	}
	return false
}

func normalizeName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

func whyHost(host, hostHead string, t entity.Application) string {
	for _, h := range t.HostCandidates {
		if h == host {
			return "host literal " + host + " também aparece em " + t.Key
		}
	}
	return "host " + hostHead + " casa com nome do serviço " + t.Key
}
