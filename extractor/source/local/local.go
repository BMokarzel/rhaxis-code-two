// Package local lê o código de um diretório do disco.
package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BMokarzel/rhaxis-code-two/extractor/source"
)

var defaultIgnore = map[string]bool{
	"node_modules": true, "dist": true, "build": true, "coverage": true, "vendor": true,
}

type Provider struct {
	Root string
	// Languages mapeia extensão para linguagem; arquivos com outras extensões são ignorados
	Languages map[string]string
	Ignore    map[string]bool
}

func New(root string, languages map[string]string) *Provider {
	return &Provider{Root: root, Languages: languages, Ignore: defaultIgnore}
}

func (p *Provider) Walk(ctx context.Context, fn func(source.File) error) error {
	return filepath.WalkDir(p.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != p.Root && (p.Ignore[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".d.ts") {
			return nil
		}
		lang, ok := p.Languages[filepath.Ext(name)]
		if !ok {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(p.Root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		return fn(source.File{
			Path:     filepath.ToSlash(rel),
			Language: lang,
			Hash:     hex.EncodeToString(sum[:]),
			Content:  content,
		})
	})
}

func (p *Provider) ReadFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(filepath.Join(p.Root, filepath.FromSlash(path)))
}
