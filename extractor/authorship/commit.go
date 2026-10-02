package authorship

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// commitInfo é o que extraímos de um commit do git. Datas em RFC3339.
type commitInfo struct {
	SHA          string
	Author       identity
	Committer    identity
	AuthoredAt   string
	CommittedAt  string
	ParentSHA    string
	Subject      string
}

// git log --use-mailmap --pretty=format:'<header>' <range>
//
// Separador de campo: 0x1F (unit separator). Nenhum campo retornado pelo git
// contém 0x1F na prática. Uma linha por commit; subject é uma linha só.
const commitFormat = "%H%x1F%an%x1F%ae%x1F%cn%x1F%ce%x1F%aI%x1F%cI%x1F%P%x1F%s"

func parseLog(output string) ([]commitInfo, error) {
	var out []commitInfo
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1F")
		if len(parts) != 9 {
			return nil, fmt.Errorf("git log: linha com %d campos, esperado 9: %q", len(parts), line)
		}
		info := commitInfo{
			SHA:         parts[0],
			Author:      normalizeIdentity(parts[1], parts[2]),
			Committer:   normalizeIdentity(parts[3], parts[4]),
			AuthoredAt:  parts[5],
			CommittedAt: parts[6],
			Subject:     parts[8],
		}
		// %P devolve todos os parents separados por espaço; mantemos só o primeiro
		// (first-parent linear, como combinado no plano).
		if ps := strings.Fields(parts[7]); len(ps) > 0 {
			info.ParentSHA = ps[0]
		}
		out = append(out, info)
	}
	return out, nil
}

func runGitLog(ctx context.Context, repo, rangeExpr, subdir string, limit int) ([]commitInfo, error) {
	args := []string{"-C", repo, "log", "--use-mailmap", "--date-order", "--pretty=format:" + commitFormat}
	if limit > 0 {
		args = append(args, "-n", strconv.Itoa(limit))
	}
	if rangeExpr != "" {
		args = append(args, rangeExpr)
	}
	if subdir != "" && subdir != "." {
		args = append(args, "--", subdir)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git log: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseLog(out.String())
}

// changedFile é um par (status, path) devolvido por `git diff-tree`.
// renamedFrom é preenchido quando status é R/C.
type changedFile struct {
	Status      string // A, M, D, R, C, T (type change)
	Path        string
	RenamedFrom string
}

// diff-tree -r --no-commit-id --name-status -z -M -C --root <sha>
// -z usa NUL como separador para suportar paths com espaço/tab.
// --root faz com que o commit inicial (sem parent) devolva todos os arquivos
// como "A", em vez de output vazio.
func runDiffTree(ctx context.Context, repo, sha, subdir string) ([]changedFile, error) {
	args := []string{"-C", repo, "diff-tree", "-r", "--no-commit-id", "--name-status", "-z", "-M", "-C", "--root", sha}
	if subdir != "" && subdir != "." {
		args = append(args, "--", subdir)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff-tree %s: %w: %s", sha, err, strings.TrimSpace(stderr.String()))
	}
	return parseDiffTree(out.Bytes(), subdir)
}

func parseDiffTree(data []byte, subdir string) ([]changedFile, error) {
	var out []changedFile
	prefix := ""
	if subdir != "" && subdir != "." {
		prefix = strings.TrimSuffix(subdir, "/") + "/"
	}
	fields := bytes.Split(data, []byte{0})
	i := 0
	for i < len(fields) {
		status := string(fields[i])
		if status == "" {
			i++
			continue
		}
		i++
		if i >= len(fields) {
			break
		}
		letter := status[:1]
		cf := changedFile{Status: letter}
		if letter == "R" || letter == "C" {
			// Rename/Copy: dois paths (from, to).
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("diff-tree: faltou destino de %s", status)
			}
			cf.RenamedFrom = string(fields[i])
			cf.Path = string(fields[i+1])
			i += 2
		} else {
			cf.Path = string(fields[i])
			i++
		}
		if prefix != "" {
			if !strings.HasPrefix(cf.Path, prefix) {
				continue
			}
			cf.Path = cf.Path[len(prefix):]
			if cf.RenamedFrom != "" && strings.HasPrefix(cf.RenamedFrom, prefix) {
				cf.RenamedFrom = cf.RenamedFrom[len(prefix):]
			}
		}
		out = append(out, cf)
	}
	return out, nil
}

// statusToKind traduz a letra do git diff-tree para o nosso vocabulário em
// ChangedEdge.Kind. "T" (type change) e "C" (copy) são preservados; a letra
// desconhecida vira "touched".
func statusToKind(status string) string {
	switch status {
	case "A":
		return "added"
	case "M":
		return "modified"
	case "D":
		return "deleted"
	case "R":
		return "renamed"
	case "C":
		return "copied"
	case "T":
		return "type_changed"
	default:
		return "touched"
	}
}
