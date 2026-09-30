// Package javascript resolve especificadores de import de JavaScript/TypeScript para arquivos
// da aplicação ou pacotes externos, respeitando baseUrl e paths do tsconfig.json.
package javascript

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/BMokarzel/rhaxis-code-two/linker"
)

var extensions = []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}

type alias struct {
	prefix, suffix string
	wildcard       bool
	targets        []string // relativos à raiz, com "*" no lugar do curinga
}

type Resolver struct {
	files   map[string]bool
	baseURL string // relativo à raiz; "" quando não configurado
	aliases []alias
}

// NewFactory cria uma factory que lê o tsconfig.json da raiz da aplicação, se existir
func NewFactory() linker.ResolverFactory {
	return func(ctx context.Context, in linker.Input) (linker.ModuleResolver, error) {
		files := map[string]bool{}
		for _, fg := range in.Files {
			files[fg.File.Path] = true
		}
		r := &Resolver{files: files}
		if in.ReadFile != nil {
			r.loadTSConfig(ctx, in.ReadFile, "tsconfig.json", map[string]bool{})
		}
		return r, nil
	}
}

func NewResolver(files []string, baseURL string, paths map[string][]string) *Resolver {
	r := &Resolver{files: map[string]bool{}, baseURL: baseURL}
	for _, f := range files {
		r.files[f] = true
	}
	r.setPaths(paths, baseURL)
	return r
}

type tsconfig struct {
	Extends         string `json:"extends"`
	CompilerOptions struct {
		BaseURL *string             `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	} `json:"compilerOptions"`
}

// loadTSConfig aplica primeiro a configuração herdada (extends) e depois a do próprio arquivo
func (r *Resolver) loadTSConfig(ctx context.Context, read func(context.Context, string) ([]byte, error), file string, seen map[string]bool) {
	if seen[file] {
		return
	}
	seen[file] = true
	raw, err := read(ctx, file)
	if err != nil {
		return
	}
	var cfg tsconfig
	if err := json.Unmarshal(StripJSONC(raw), &cfg); err != nil {
		return
	}
	dir := path.Dir(file)
	if strings.HasPrefix(cfg.Extends, ".") {
		ext := path.Join(dir, cfg.Extends)
		if !strings.HasSuffix(ext, ".json") {
			ext += ".json"
		}
		r.loadTSConfig(ctx, read, ext, seen)
	}
	if cfg.CompilerOptions.BaseURL != nil {
		r.baseURL = cleanRel(path.Join(dir, *cfg.CompilerOptions.BaseURL))
	}
	if cfg.CompilerOptions.Paths != nil {
		base := dir
		if cfg.CompilerOptions.BaseURL != nil || r.baseURL != "" {
			base = r.baseURL
		}
		r.aliases = nil
		r.setPaths(cfg.CompilerOptions.Paths, base)
	}
}

func (r *Resolver) setPaths(paths map[string][]string, base string) {
	for pattern, targets := range paths {
		a := alias{prefix: pattern}
		if i := strings.Index(pattern, "*"); i >= 0 {
			a.prefix, a.suffix, a.wildcard = pattern[:i], pattern[i+1:], true
		}
		for _, t := range targets {
			a.targets = append(a.targets, cleanRel(path.Join(base, t)))
		}
		r.aliases = append(r.aliases, a)
	}
}

func (r *Resolver) Resolve(fromPath, spec string) (linker.Target, bool) {
	if spec == "" {
		return linker.Target{}, false
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || spec == "." || spec == ".." {
		if f, ok := r.candidates(cleanRel(path.Join(path.Dir(fromPath), spec))); ok {
			return linker.Target{FilePath: f}, true
		}
		return linker.Target{}, false
	}
	for _, a := range r.aliases {
		wild, ok := match(a, spec)
		if !ok {
			continue
		}
		for _, t := range a.targets {
			if f, found := r.candidates(strings.Replace(t, "*", wild, 1)); found {
				return linker.Target{FilePath: f}, true
			}
		}
	}
	if r.baseURL != "" {
		if f, ok := r.candidates(cleanRel(path.Join(r.baseURL, spec))); ok {
			return linker.Target{FilePath: f}, true
		}
	}
	return linker.Target{Package: packageName(spec)}, true
}

func match(a alias, spec string) (string, bool) {
	if !a.wildcard {
		return "", spec == a.prefix
	}
	if !strings.HasPrefix(spec, a.prefix) || !strings.HasSuffix(spec, a.suffix) || len(spec) < len(a.prefix)+len(a.suffix) {
		return "", false
	}
	return spec[len(a.prefix) : len(spec)-len(a.suffix)], true
}

func (r *Resolver) candidates(p string) (string, bool) {
	if r.files[p] {
		return p, true
	}
	// import "./x.js" em projeto TS aponta para x.ts
	for _, js := range []string{".js", ".jsx", ".mjs", ".cjs"} {
		if strings.HasSuffix(p, js) {
			stem := strings.TrimSuffix(p, js)
			for _, ext := range extensions {
				if r.files[stem+ext] {
					return stem + ext, true
				}
			}
		}
	}
	for _, ext := range extensions {
		if r.files[p+ext] {
			return p + ext, true
		}
	}
	for _, ext := range extensions {
		if f := path.Join(p, "index"+ext); r.files[f] {
			return f, true
		}
	}
	return "", false
}

// packageName reduz "lodash/fp" a "lodash" e "@nestjs/common/x" a "@nestjs/common"
func packageName(spec string) string {
	parts := strings.Split(spec, "/")
	if strings.HasPrefix(spec, "@") && len(parts) > 1 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func cleanRel(p string) string {
	p = path.Clean(p)
	return strings.TrimPrefix(p, "./")
}

// StripJSONC remove comentários e vírgulas finais, aceitos no tsconfig.json
func StripJSONC(in []byte) []byte {
	out := make([]byte, 0, len(in))
	inString := false
	for i := 0; i < len(in); i++ {
		c := in[i]
		if inString {
			out = append(out, c)
			if c == '\\' && i+1 < len(in) {
				i++
				out = append(out, in[i])
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out = append(out, c)
		case c == '/' && i+1 < len(in) && in[i+1] == '/':
			for i < len(in) && in[i] != '\n' {
				i++
			}
			if i < len(in) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(in) && in[i+1] == '*':
			i += 2
			for i+1 < len(in) && !(in[i] == '*' && in[i+1] == '/') {
				i++
			}
			i++
		case c == ',':
			j := i + 1
			for j < len(in) && (in[j] == ' ' || in[j] == '\n' || in[j] == '\r' || in[j] == '\t') {
				j++
			}
			if j < len(in) && (in[j] == '}' || in[j] == ']') {
				continue
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
