package javascript

import (
	"context"
	"testing"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor/ir"
	"github.com/BMokarzel/rhaxis-code-two/extractor/linker"
)

func TestResolve(t *testing.T) {
	r := NewResolver([]string{
		"src/users/users.service.ts",
		"src/users/index.ts",
		"src/shared/logger.ts",
		"src/app.ts",
	}, "", map[string][]string{"@shared/*": {"src/shared/*"}})

	cases := []struct {
		from, spec string
		want       linker.Target
	}{
		{"src/app.ts", "./users/users.service", linker.Target{FilePath: "src/users/users.service.ts"}},
		{"src/app.ts", "./users", linker.Target{FilePath: "src/users/index.ts"}},
		{"src/users/index.ts", "./users.service.js", linker.Target{FilePath: "src/users/users.service.ts"}},
		{"src/users/users.service.ts", "../shared/logger", linker.Target{FilePath: "src/shared/logger.ts"}},
		{"src/app.ts", "@shared/logger", linker.Target{FilePath: "src/shared/logger.ts"}},
		{"src/app.ts", "@nestjs/common", linker.Target{Package: "@nestjs/common"}},
		{"src/app.ts", "lodash/fp", linker.Target{Package: "lodash"}},
		{"src/app.ts", "node:fs/promises", linker.Target{Package: "node:fs"}},
	}
	for _, c := range cases {
		got, ok := r.Resolve(c.from, c.spec)
		if !ok || got != c.want {
			t.Errorf("Resolve(%q, %q) = %+v, %v; quer %+v", c.from, c.spec, got, ok, c.want)
		}
	}
	if _, ok := r.Resolve("src/app.ts", "./missing"); ok {
		t.Error("import relativo inexistente não deveria resolver")
	}
}

func TestTSConfig(t *testing.T) {
	configs := map[string]string{
		"tsconfig.json": `{
			// comentário
			"extends": "./tsconfig.base.json",
			"compilerOptions": { "baseUrl": "./", },
		}`,
		"tsconfig.base.json": `{ "compilerOptions": { "baseUrl": "src", "paths": { "@app/*": ["app/*"] } } }`,
	}
	read := func(_ context.Context, p string) ([]byte, error) { return []byte(configs[p]), nil }
	in := linker.Input{
		Files: []*ir.FileGraph{
			{File: entity.File{Path: "src/app/users.ts"}},
			{File: entity.File{Path: "src/main.ts"}},
		},
		ReadFile: read,
	}
	res, err := NewFactory()(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := res.Resolve("src/main.ts", "@app/users"); got.FilePath != "src/app/users.ts" {
		t.Errorf("alias do tsconfig herdado: %+v", got)
	}
	if got, _ := res.Resolve("src/app/users.ts", "src/main"); got.FilePath != "src/main.ts" {
		t.Errorf("baseUrl: %+v", got)
	}
}
