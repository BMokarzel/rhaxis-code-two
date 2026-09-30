# Arquitetura da extração

Decisões que atualizam o `graph-model-review.md`.

## Modelo

- **Declaração é um node; uso é uma edge.** Não existe node `Reference`. `f(x)` gera
  `Call -ARGUMENT{0}-> Variable x`; a posição do uso fica na propriedade `location` da edge.
- **Expressões são achatadas.** `Operation`, `Literal`, `MemberAccess` e `ObjectLiteral` não
  viram nodes. `f(a + b)` gera `ARGUMENT{0}` para `a` e para `b`. `user.email` aponta para o
  `Field email` quando o tipo de `user` é conhecido; senão aponta para `user` com
  `member: "email"`.
- **Import não é node.** Imports e exports existem só no formato intermediário (`ir`); o linker
  os consome e no grafo fica `File -IMPORTS-> File | Package`.
- **Uma chamada sem alvo conhecido não gera `CALLS` falso.** `this.repo.save()` com `repo` sem tipo
  vira `READS` para o campo `repo` com `member: "save"`.
- Nomes que não resolvem viram um node `External` (por aplicação); pacotes viram `Package`
  (ID global `pkg:<nome>`, para permitir `PROVIDED_BY` entre aplicações).
- Decorators (Nest) ficam como texto bruto em `Decorators` de Class, Function, Parameter e Field;
  as regras de contrato (Endpoint, HttpParam) vão ler dali.

## IDs

- File: `<appKey>:<path>`
- Declarações: `<fileID>#<nome qualificado>` (`...users.service.ts#UsersService.findOne`),
  estáveis entre edições. Parâmetros: `...#UsersService.findOne(id)`.
- Nodes internos (Call, funções anônimas): posição (`#UsersService.findOne/call@14:24`).

## Módulos

```
cmd/rhaxis            CLI: extrai um diretório e grava JSON
orchestrator/         captura → extração (paralela) → linkagem → persistência
source/               interface Provider; source/local lê do disco
extractor/            interface Extractor + registry por linguagem
extractor/javascript  tree-sitter (js, jsx, ts, tsx) → ir.FileGraph
ir/                   formato intermediário: contrato entre extrator e linker
linker/               resolução genérica: escopos, exports, membros de classe, tipos
linker/javascript     resolução de módulos: relativos, index, tsconfig paths/baseUrl
repository/           interface GraphRepository; repository/memory para testes e JSON
entity/               nodes e edges persistidos
```

O orquestrador conhece apenas interfaces. Para trocar a origem do código, implemente
`source.Provider`. Para trocar o banco, implemente `repository.GraphRepository`. Para usar um
extrator escrito em outra linguagem, implemente `Extractor` chamando um processo externo que
devolva o `ir.FileGraph`.

## Estado

M1 está escrito: estrutura, declarações, chamadas, leituras, escritas e argumentos, resolução
entre arquivos (imports, re-exports, barrels, alias do tsconfig, CommonJS `require`), tipos
declarados e inferidos por `new`, `INVOKES`.

**Pendente de validação:** `extractor/javascript` e `cmd/rhaxis` ainda não foram compilados,
porque as dependências do tree-sitter não puderam ser baixadas na máquina onde o código foi
escrito. `entity`, `ir`, `source`, `linker`, `repository` e `orchestrator` compilam, e os testes
de `linker/...` passam.

## Próximos passos

### M1: fechar a validação

- [ ] Baixar as dependências (exige cgo, ou seja, `gcc` instalado):
      `go get github.com/tree-sitter/go-tree-sitter@latest github.com/tree-sitter/tree-sitter-typescript@latest github.com/tree-sitter/tree-sitter-javascript@latest && go mod tidy`
- [ ] `go build ./...` e corrigir divergências da API do `go-tree-sitter` (tipos de índice
      `uint`/`uint32`, nomes das funções de linguagem nos bindings).
- [ ] `go test ./...` e ajustar os nomes de node das grammars onde o extrator divergir. Os pontos
      mais prováveis são decorators (filhos do node × irmãos no `class_body`), `class_heritage`,
      `required_parameter` com `readonly`/`accessibility_modifier` e o campo `kind` de
      `lexical_declaration`/`for_in_statement`.
- [ ] Rodar `go run ./cmd/rhaxis -path testdata/nest-sample -out nest.graph.json` e revisar o
      JSON à mão.
- [ ] Rodar em um projeto Nest real e anotar o que ficou `External` ou sem `CALLS` indevidamente.
- [ ] Commit do M1.

### M2: contrato e fluxo

- [ ] `Endpoint` e `HttpParam` a partir dos decorators do Nest (`@Controller`, `@Get`, `@Param`,
      `@Query`, `@Body`, `@Headers`), com path normalizado (`/users/{id}`), `EXPOSES`,
      `HANDLED_BY`, `HAS_PARAM` e `BINDS`.
- [ ] Rotas do Express (`router.get('/x', handler)`).
- [ ] `Return` e `Assignment` como nodes, e a derivação de `FLOWS_TO` (argumento → parâmetro,
      retorno → chamada, atribuição → variável, `for..of`).
- [ ] Injeção de dependência do Nest por token (`@Inject(TOKEN)`, `useClass`/`useFactory` nos
      módulos).
- [ ] Leitura de `module.exports`/`exports.x` do CommonJS como export.

### M3: persistência e incremental

- [ ] `repository/neo4j` implementando `GraphRepository` com o driver v5, usando
      `entity.Properties()` para os nodes e `MERGE` por ID, com índices por `id` e `fileId`.
- [ ] Extração incremental: comparar `File.Hash`, re-extrair só os arquivos alterados e re-linkar
      os arquivos que os importam. Exige guardar o `ir.FileGraph` (ou relinkar a partir do banco).
- [ ] Nodes pelos quais a travessia de impacto não deve se propagar (logger, utilitários).

### Depois

- [ ] `REQUESTS` entre aplicações (clientes HTTP casados com `Endpoint`), `PROVIDED_BY` e
      `DEPENDS_ON`.
- [ ] Controle de fluxo (`If`, `Loop`, `Try`, `NEXT`) e `Telemetry`/`EMITS`.
- [ ] Um extrator de outra linguagem, para validar que o `ir` é genérico.

## Limitações conhecidas

- `module.exports` não é lido como export; o linker cai no nome de topo com resolução `inferred`.
- A cadeia de membros não atravessa o retorno de chamadas (`a().b()`).
- Não há controle de fluxo.
