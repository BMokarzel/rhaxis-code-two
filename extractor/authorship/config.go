// Package authorship enriquece o grafo da aplicação com autoria vinda do git:
// Commit, Person, AUTHORED_BY/COMMITTED_BY e CHANGED (Commit → File).
//
// Sprint 2 cobre identidade + histórico por arquivo. Sprint 3 adiciona blame
// (OWNS/CREATED/LAST_MODIFIED por declaração) e Teams.
package authorship

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// StoreEmail controla como o email do autor é persistido.
type StoreEmail string

const (
	StoreEmailPlain StoreEmail = "plain" // email bruto (default)
	StoreEmailHash  StoreEmail = "hash"  // sha256(email) — não armazena PII
	StoreEmailNone  StoreEmail = "none"  // omite o campo
)

// Config é a configuração mínima de autoria. Pode vir de .rhaxis.yaml (seção
// authorship) ou de flags de CLI (CLI tem precedência). Zero value = authorship off.
type Config struct {
	Enabled      bool       `yaml:"enabled"`
	StoreEmail   StoreEmail `yaml:"store_email"`
	GithubLogin  bool       `yaml:"github_login"`
	IgnoreRevs   string     `yaml:"ignore_revs"` // path para .git-blame-ignore-revs (usado em Sprint 3)
	CommitsRange string     `yaml:"commits_range"`
	CommitsLimit int        `yaml:"commits_limit"`
}

// Defaults aplica valores padrão em cima de uma Config parcial.
func (c *Config) ApplyDefaults() {
	if c.StoreEmail == "" {
		c.StoreEmail = StoreEmailPlain
	}
}

func (c Config) Validate() error {
	switch c.StoreEmail {
	case "", StoreEmailPlain, StoreEmailHash, StoreEmailNone:
	default:
		return fmt.Errorf("authorship.store_email: valor inválido %q (use plain|hash|none)", c.StoreEmail)
	}
	if c.CommitsLimit < 0 {
		return fmt.Errorf("authorship.commits_limit: deve ser >= 0")
	}
	return nil
}

// fileConfig é o schema parcial do .rhaxis.yaml que esta sprint consome. Outros
// campos (libraries, services, ...) serão lidos por pacotes dedicados em sprints
// futuras; aqui só interessa a seção authorship.
type fileConfig struct {
	Authorship Config `yaml:"authorship"`
}

// LoadFile lê .rhaxis.yaml e devolve apenas a seção authorship com defaults
// aplicados. Se o arquivo não existe, retorna Config zerada sem erro — authorship
// fica desligada por omissão.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			var c Config
			c.ApplyDefaults()
			return c, nil
		}
		return Config{}, fmt.Errorf("lendo %s: %w", path, err)
	}
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	fc.Authorship.ApplyDefaults()
	if err := fc.Authorship.Validate(); err != nil {
		return Config{}, err
	}
	return fc.Authorship, nil
}
