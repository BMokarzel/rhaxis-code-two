// Package source define de onde vem o código (disco local, git, API de um provedor...).
package source

import "context"

type File struct {
	// Path é relativo à raiz da aplicação, sempre com "/"
	Path     string
	Language string
	Hash     string
	Content  []byte
}

type Provider interface {
	// Walk chama fn para cada arquivo de código da aplicação
	Walk(ctx context.Context, fn func(File) error) error
	// ReadFile lê um arquivo qualquer da aplicação (tsconfig.json, package.json...)
	ReadFile(ctx context.Context, path string) ([]byte, error)
}
