package authorship

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// blameLine é a atribuição de uma linha final do arquivo a um commit/autor.
type blameLine struct {
	FinalLine int       // número da linha no arquivo final (1-based)
	SHA       string    // commit SHA que introduziu a linha
	Author    identity  // email normalizado
	Time      int64     // author-time unix
}

// runBlame executa `git blame --porcelain -w -M -C <rev> -- <path>`
// e devolve uma linha por linha do arquivo, com autor e commit atribuídos.
// -w ignora whitespace, -M detecta movimentação intra-arquivo, -C entre arquivos.
// mailmap aqui é opt-in via config do repo (blame.mailmap=true); nem toda
// versão de git aceita a flag CLI.
func runBlame(ctx context.Context, repo, rev, path string) ([]blameLine, error) {
	args := []string{"-C", repo, "blame", "--porcelain", "-w", "-M", "-C"}
	if rev != "" {
		args = append(args, rev)
	}
	args = append(args, "--", path)
	cmd := exec.CommandContext(ctx, "git", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git blame %s %s: %w: %s", rev, path, err, strings.TrimSpace(stderr.String()))
	}
	return parseBlame(out.Bytes())
}

// commitMeta é cacheada por SHA: no formato --porcelain, metadados vêm só na
// primeira ocorrência de cada commit; linhas subsequentes do mesmo commit só
// trazem SHA + números de linha.
type commitMeta struct {
	Author identity
	Time   int64
}

func parseBlame(data []byte) ([]blameLine, error) {
	cache := map[string]commitMeta{}
	var out []blameLine

	var curSHA string
	var curFinal int
	var curAuthorName, curAuthorMail string
	var curAuthorTime int64

	flush := func() {
		meta, cached := cache[curSHA]
		if !cached {
			meta = commitMeta{
				Author: normalizeIdentity(curAuthorName, strings.Trim(curAuthorMail, "<>")),
				Time:   curAuthorTime,
			}
			cache[curSHA] = meta
		}
		out = append(out, blameLine{
			FinalLine: curFinal,
			SHA:       curSHA,
			Author:    meta.Author,
			Time:      meta.Time,
		})
	}

	for _, raw := range bytes.Split(data, []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		// Linha de conteúdo começa com TAB — fecha o registro atual.
		if raw[0] == '\t' {
			if curSHA != "" {
				flush()
			}
			// reset dos campos de metadata; SHA e FinalLine serão sobrescritos
			// pela próxima header, mas limpamos para evitar vazamento.
			curSHA = ""
			curFinal = 0
			curAuthorName, curAuthorMail = "", ""
			curAuthorTime = 0
			continue
		}
		line := string(raw)
		if isSHAHeader(line) {
			// "<sha> <orig> <final> [<group>]"
			fields := strings.Fields(line)
			if len(fields) < 3 {
				return nil, fmt.Errorf("blame header mal formado: %q", line)
			}
			curSHA = fields[0]
			final, err := strconv.Atoi(fields[2])
			if err != nil {
				return nil, fmt.Errorf("blame header final inválido: %q", line)
			}
			curFinal = final
			// Se já conhecemos esse SHA, não virão campos author/time; não zeramos
			// o cache. Se é novo, limpamos buffers para capturar os próximos.
			if _, cached := cache[curSHA]; !cached {
				curAuthorName, curAuthorMail = "", ""
				curAuthorTime = 0
			}
			continue
		}
		// Campos relevantes:
		switch {
		case strings.HasPrefix(line, "author-mail "):
			curAuthorMail = strings.TrimPrefix(line, "author-mail ")
		case strings.HasPrefix(line, "author "):
			curAuthorName = strings.TrimPrefix(line, "author ")
		case strings.HasPrefix(line, "author-time "):
			if ts, err := strconv.ParseInt(strings.TrimPrefix(line, "author-time "), 10, 64); err == nil {
				curAuthorTime = ts
			}
		}
		// outros campos (committer, summary, filename, previous) ignorados
	}
	return out, nil
}

func isSHAHeader(line string) bool {
	// SHA + espaço + dois números (orig, final) + opcionalmente mais um
	if len(line) < 44 {
		return false
	}
	if line[40] != ' ' {
		return false
	}
	for i := 0; i < 40; i++ {
		c := line[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
