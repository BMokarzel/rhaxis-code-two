package crosservice

import (
	"strings"

	"github.com/BMokarzel/rhaxis-code-two/entity"
)

// resolveEnv faz R2 do plano: para cada env var que a app `src` lê, procura entre
// as apps `targets` quem o prefixo da env sugere.
//
// Score:
//
//	+2 — target.EnvVarsRead contém a mesma env (apps "irmãs" compartilham env;
//	     sinal forte quando acontece)
//	+1 — prefixo da env (até o primeiro `_`) casa com Key/Name do target
//	     Ex.: `USERS_URL` → prefixo `USERS` → casa com app `users*`
func resolveEnv(src entity.Application, targets []entity.Application) []envMatch {
	var out []envMatch
	for _, env := range src.EnvVarsRead {
		prefix := envPrefix(env)
		var hits []scoredApp
		for _, t := range targets {
			if t.ID() == src.ID() {
				continue
			}
			score := scoreEnv(env, prefix, t)
			if score > 0 {
				hits = append(hits, scoredApp{App: t, Score: score, Why: whyEnv(env, prefix, t)})
			}
		}
		// Emite inclusive quando hits está vazio — o chamador conta como Unmatched.
		out = append(out, envMatch{Env: env, Candidates: hits})
	}
	return out
}

type envMatch struct {
	Env        string
	Candidates []scoredApp
}

// envPrefix pega o primeiro componente antes de `_`. Ex.: USERS_URL → USERS.
func envPrefix(env string) string {
	if i := strings.IndexByte(env, '_'); i > 0 {
		return env[:i]
	}
	return env
}

func scoreEnv(env, prefix string, t entity.Application) int {
	s := 0
	for _, e := range t.EnvVarsRead {
		if e == env {
			s += 2
			break
		}
	}
	if s > 0 {
		return s
	}
	if prefix == "" {
		return 0
	}
	if nameMatch(strings.ToLower(prefix), t.Key) || nameMatch(strings.ToLower(prefix), t.Name) {
		s += 1
	}
	return s
}

func whyEnv(env, prefix string, t entity.Application) string {
	for _, e := range t.EnvVarsRead {
		if e == env {
			return "env " + env + " também é lida por " + t.Key
		}
	}
	return "prefixo " + prefix + " casa com nome do serviço " + t.Key
}
