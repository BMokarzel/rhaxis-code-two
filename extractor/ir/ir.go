// Package ir define o formato intermediário produzido por um extrator a partir de UM arquivo.
//
// É o contrato entre extração e linkagem: o extrator não olha outros arquivos, então tudo que
// depende de nome (usos de variáveis, chamadas, tipos) sai como PendingRef, e o linker transforma
// em edges. Scopes, Imports e Exports existem só aqui; não são persistidos.
package ir

import "github.com/BMokarzel/rhaxis-code-two/entity"

type FileGraph struct {
	File entity.File
	// Nodes e Edges já resolvidos dentro do arquivo (estrutura: DECLARES, HAS_PARAMETER, CONTAINS...)
	Nodes []entity.Node
	Edges []entity.Edge

	// FileScopeID é o escopo de topo do arquivo
	FileScopeID string
	Scopes      []Scope
	Refs        []PendingRef
	Imports     []Import
	Exports     []Export
}

// Scope é um escopo léxico (arquivo, função, bloco, classe)
type Scope struct {
	ID       string
	ParentID string
	// Names mapeia o nome visível no escopo para o ID do node que o declara.
	// "this" é registrado nos escopos de método apontando para a classe.
	Names map[string]string
}

// PendingRef é um uso por nome que o linker precisa resolver.
type PendingRef struct {
	From    string
	Edge    entity.EdgeType
	ScopeID string
	// Name é a raiz do uso; Path é a cadeia de membros: this.repo.find → Name "this", Path [repo find]
	Name  string
	Path  []string
	Index *int
	Loc   entity.Location
	// Operator das escritas (=, +=, ++)
	Operator string
	// Member rastreia o key/index de origem em destructuring (`const {a} = obj` → Member="a";
	// `[x, y] = arr` → Member="0" no WRITES de x). Usado só para WRITES por destructuring —
	// nas resoluções normais, Member é derivado do caminho resolvido pelo linker.
	Member string
	// Inferred marca refs vindas de inferência do extrator (ex.: tipo a partir de `new X()`)
	Inferred bool
}

type ImportKind string

const (
	ImportNamed      = ImportKind("named")      // import { a as b } from
	ImportDefault    = ImportKind("default")    // import a from
	ImportNamespace  = ImportKind("namespace")  // import * as a from; const a = require()
	ImportSideEffect = ImportKind("sideeffect") // import './x'
)

type Import struct {
	// Local é o nome visível no arquivo; Imported é o nome exportado pelo módulo de origem
	Local    string
	Imported string
	Source   string
	Kind     ImportKind
	Loc      entity.Location
}

type ExportKind string

const (
	ExportLocal       = ExportKind("local")        // export function f / export { a as b }
	ExportReexport    = ExportKind("reexport")     // export { a as b } from './x'
	ExportReexportAll = ExportKind("reexport_all") // export * from './x'
)

type Export struct {
	Kind ExportKind
	// Name é o nome visto por quem importa ("default" para export default)
	Name string
	// Local é o nome dentro do arquivo (ExportLocal) ou no módulo de origem (ExportReexport)
	Local string
	// NodeID é preenchido quando o extrator já sabe qual declaração é exportada
	NodeID string
	Source string
}
