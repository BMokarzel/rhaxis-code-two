// Package git lê o código de uma revisão (SHA, branch, tag) sem precisar de checkout.
// Usa `git ls-tree` para listar blobs e `git cat-file --batch` para lê-los em lote.
package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BMokarzel/rhaxis-code-two/extractor/source"
)

var defaultIgnore = map[string]bool{
	"node_modules": true, "dist": true, "build": true, "coverage": true, "vendor": true,
}

type Provider struct {
	Repo      string            // caminho do repositório (dir contendo .git)
	Rev       string            // SHA, branch, tag; vazio = HEAD
	Subdir    string            // subpasta do monorepo; vazio ou "." = raiz
	Languages map[string]string // mesmo map que source/local
	Ignore    map[string]bool
}

func New(repo, rev, subdir string, languages map[string]string) *Provider {
	if rev == "" {
		rev = "HEAD"
	}
	return &Provider{
		Repo:      repo,
		Rev:       rev,
		Subdir:    subdir,
		Languages: languages,
		Ignore:    defaultIgnore,
	}
}

type entry struct {
	sha  string // blob SHA
	path string // relativo a Subdir (para bater com source/local)
}

func (p *Provider) listTree(ctx context.Context) ([]entry, error) {
	args := []string{"-C", p.Repo, "ls-tree", "-r", "-z", "--full-tree", p.Rev}
	if p.Subdir != "" && p.Subdir != "." {
		args = append(args, "--", p.Subdir)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git ls-tree: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	var prefix string
	if p.Subdir != "" && p.Subdir != "." {
		prefix = strings.TrimSuffix(filepath.ToSlash(p.Subdir), "/") + "/"
	}

	var entries []entry
	data := out.Bytes()
	for len(data) > 0 {
		i := bytes.IndexByte(data, 0)
		if i < 0 {
			break
		}
		line := string(data[:i])
		data = data[i+1:]
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		header := line[:tab]
		full := line[tab+1:]
		parts := strings.Fields(header)
		if len(parts) != 3 || parts[1] != "blob" {
			continue
		}
		rel := full
		if prefix != "" {
			if !strings.HasPrefix(full, prefix) {
				continue
			}
			rel = full[len(prefix):]
		}
		entries = append(entries, entry{sha: parts[2], path: rel})
	}
	return entries, nil
}

// shouldInclude replica os filtros de source/local: ignora diretórios ocultos ou
// na lista Ignore, descarta .d.ts e extensões desconhecidas. Arquivos com nome
// começando por "." não são filtrados (bate com local, que só pula dirs ocultos).
func (p *Provider) shouldInclude(path string) (string, bool) {
	if strings.HasSuffix(path, ".d.ts") {
		return "", false
	}
	parts := strings.Split(path, "/")
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		if p.Ignore[part] || strings.HasPrefix(part, ".") {
			return "", false
		}
	}
	name := parts[len(parts)-1]
	lang, ok := p.Languages[filepath.Ext(name)]
	if !ok {
		return "", false
	}
	return lang, true
}

func (p *Provider) Walk(ctx context.Context, fn func(source.File) error) error {
	entries, err := p.listTree(ctx)
	if err != nil {
		return err
	}
	type keep struct {
		entry
		lang string
	}
	var filtered []keep
	for _, e := range entries {
		lang, ok := p.shouldInclude(e.path)
		if !ok {
			continue
		}
		filtered = append(filtered, keep{entry: e, lang: lang})
	}
	if len(filtered) == 0 {
		return nil
	}

	cmd := exec.CommandContext(ctx, "git", "-C", p.Repo, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()

	reader := bufio.NewReader(stdout)
	for _, k := range filtered {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := io.WriteString(stdin, k.sha+"\n"); err != nil {
			return fmt.Errorf("cat-file write: %w", err)
		}
		header, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("cat-file read header: %w (stderr=%s)", err, strings.TrimSpace(stderr.String()))
		}
		header = strings.TrimRight(header, "\n")
		fields := strings.Fields(header)
		if len(fields) == 2 && fields[1] == "missing" {
			return fmt.Errorf("blob ausente: %s", k.sha)
		}
		if len(fields) != 3 || fields[1] != "blob" {
			return fmt.Errorf("cat-file header inesperado: %q", header)
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return fmt.Errorf("cat-file tamanho inválido %q: %w", fields[2], err)
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(reader, content); err != nil {
			return fmt.Errorf("cat-file read content: %w", err)
		}
		// git termina cada resposta com um '\n' depois do conteúdo
		if _, err := reader.ReadByte(); err != nil {
			return fmt.Errorf("cat-file trailing newline: %w", err)
		}
		if err := fn(source.File{
			Path:     k.path,
			Language: k.lang,
			Hash:     k.sha,
			Content:  content,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provider) ReadFile(ctx context.Context, path string) ([]byte, error) {
	target := filepath.ToSlash(path)
	if p.Subdir != "" && p.Subdir != "." {
		target = strings.TrimSuffix(filepath.ToSlash(p.Subdir), "/") + "/" + target
	}
	cmd := exec.CommandContext(ctx, "git", "-C", p.Repo, "cat-file", "blob", p.Rev+":"+target)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git cat-file %s:%s: %w: %s", p.Rev, target, err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}
