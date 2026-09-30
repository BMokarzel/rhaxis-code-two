# Plano de fechamento dos gaps de extração

Cada etapa lista: **objetivo**, **arquivos alvo**, **passos**, **fixtures que validam**, **critério de aceite**.
Ordem sugerida segue custo × valor. Nada aqui muda o `entity/` sem necessidade — a maioria já tem o tipo definido, só falta emitir.

---

## Etapa 1 — Resolução de `super` e chamadas em campo com tipo `External`

**Objetivo.** Reconectar cadeia de herança executável e chamadas em `Map`/`Array`/stdlib. Hoje 8 Calls ficam órfãos (`super`, `super.set`, `this.users.get/set`, `this.data.get/set/delete`, `this.order.shift/push`).

**Arquivos alvo.**
- `extractor/linker/javascript/` — resolver de calls.

**Passos.**
1. No resolver, quando `calleeText == "super"`: subir do `ownerId` do Call até a Class que o contém, ler `extends` (`EXTENDS` edge) e emitir `CALLS Call → <ParentClass>.constructor` (ou `INVOKES` conforme a semântica atual).
2. Quando `calleeText` começa com `"super."`: mesmo caminho, mas resolvendo o método na Class pai. Se a pai não tiver o método, subir na cadeia. Emitir `CALLS Call → <ParentClass>.<method>`.
3. Quando `calleeText == "this.<field>.<method>"`: se `<field>` tem `HAS_TYPE` para External/Package/Class do projeto, tentar resolver `<method>` no target:
   - External (`Map`, `Array`, `Set`, `Promise`, `console`, `JSON`, `process`): emitir `CALLS Call → <External>` mantendo o `member: "<method>"` como propriedade da aresta. Não precisa criar Function nodes para stdlib.
   - Class do projeto: já funciona (`this.repo.findById`). Confirmar que continua.

**Fixtures.**
- fixture-a: `AppLogger.constructor` → `BaseLogger.constructor` (super), `User.constructor` → `BaseEntity.constructor` (super), `UsersRepository.findById/save` chamando `this.users.get/set` (Map).
- fixture-b: `LRUCache.constructor` → `Store.constructor` (super), `LRUCache.set` → `Store.set` (super.set) + `this.order.shift/push` + `this.data.delete`.

**Aceite.**
- Zero Calls com `calleeText` iniciando em `super` ou `this.` sem aresta `CALLS`/`INVOKES` de saída, em ambas as fixtures.
- Novo teste em `extractor/extractor_integration_test.go` que roda `MATCH (c:Call) WHERE c.calleeText STARTS WITH "super" AND NOT (c)-[:CALLS]->() RETURN count(c)` retorna 0 (versão em memória: iterar `g.Edges` filtrando por type).

---

## Etapa 2 — CommonJS `module.exports` como `EXPORTS`

**Objetivo.** Eliminar o buraco de fronteira em código JS CommonJS. Hoje fixture-b tem 0 EXPORTS.

**Arquivos alvo.**
- `extractor/parser/javascript/` (visitor de expressões de topo do arquivo).

**Passos.**
1. Detectar padrão `module.exports = <expr>` e `exports.<name> = <expr>` no top-level.
2. Para `module.exports = { A, B, C }` (object literal): para cada shorthand/property, resolver o identificador ao nó local (Function/Class/Variable) e emitir `EXPORTS File → <node>`.
3. Para `module.exports = SomeIdentifier` (default): emitir `EXPORTS File → <node>` com propriedade `default: true` na aresta.
4. Para `exports.foo = bar`: idem, sempre singular, guardar `alias: "foo"` na aresta se o alias diferir do nome do símbolo.
5. Remover ou manter o `File WRITES External module` atual — recomendo remover para não duplicar sinal.

**Fixtures.**
- fixture-b: `src/lib/utils.js` exporta 4 símbolos, `src/lib/cache.js` exporta 2, `src/lib/client.js` exporta 2, `src/index.js` exporta 1.

**Aceite.**
- `MATCH (f:File)-[:EXPORTS]->() WHERE f.id STARTS WITH "fixture-b:" RETURN count(*)` ≥ 9.
- Teste `TestFixtureBKeyRelations` passa a exigir `entity.ExportsEdge` no `mustHaveEdges` (remover o comentário atual).

---

## Etapa 3 — Re-export barrel (`export * from`, `export { X } from`)

**Objetivo.** Restaurar visibilidade dos barrels TS. Hoje `src/users/index.ts` não tem nenhuma aresta de saída.

**Arquivos alvo.**
- `extractor/parser/javascript/` (nó `export_statement` com `source`).

**Passos.**
1. `export * from './x'`: emitir `IMPORTS File → './x'` + `EXPORTS File → <cada símbolo exportado por './x'>`. A segunda parte exige uma segunda passada pós-parse (fase de linking), porque a lista de símbolos vem do arquivo alvo.
2. `export { A, B as C } from './x'`: emitir `IMPORTS File → './x'` + `EXPORTS File → <A>` e `EXPORTS File → <B>` com `alias: "C"` na aresta do B.
3. `export { A } from './x'` sem `from`: já cai no caminho existente de `export { A }` (símbolo local).
4. Cuidado com ciclo: o linker precisa lidar com barrel que reexporta barrel. Marcar arquivos já visitados.

**Fixtures.**
- fixture-a: `src/users/index.ts` deve gerar 3 IMPORTS + EXPORTS de `UsersService`, `UsersController` (via `export *`) e `CreateUserDto` (nomeado).

**Aceite.**
- `MATCH (f:File {id:"fixture-a:src/users/index.ts"})-[:EXPORTS]->() RETURN count(*)` ≥ 3.
- `IMPORTS` do index.ts para os 3 arquivos alvo.

---

## Etapa 4 — `BINDS`: bindings de import por nome

**Objetivo.** Registrar quais símbolos específicos atravessam a fronteira em `import { A, B }` e `const { A } = require(...)`.

**Modelo.**
- `entity/edge.go` já tem `BindsEdge`. Verificar se está no `emittedEdgeTypes` (não está hoje).

**Arquivos alvo.**
- `extractor/parser/javascript/` (imports e require-destructure).
- `extractor/linker/javascript/` (resolver o binding para o símbolo remoto quando possível).

**Passos.**
1. Import ES: para cada named-import `import { A, B as C } from './x'`, criar um nó local (ou usar o próprio File como origem) e emitir `BINDS File → <símbolo em './x'>` com `alias: "C"` na aresta do segundo.
2. Import default: `import X from './x'` → `BINDS File → <default export do './x'>`.
3. `require` com destructure: `const { readFile } = require('node:fs/promises')` → `BINDS File → <External readFile>` marcando `via: "node:fs"` ou similar. Para stdlib, criar External `readFile` sob a package `node:fs`.
4. Manter `IMPORTS` como está (aresta agregada por File/Package) — `BINDS` é fino, `IMPORTS` é grosso. Consumidor escolhe.

**Fixtures.**
- fixture-a: `import { Injectable, NotFoundException } from '@nestjs/common'` gera 2 `BINDS`. `import { CreateUserDto } from './dto/create-user.dto'` gera 1.
- fixture-b: `const { readFile } = require('node:fs/promises')` gera 1. `const { loadConfig, transform } = require('./lib/client')` gera 2.

**Aceite.**
- Cobertura: `BINDS` presente no `emittedEdgeTypes` do teste de integração.
- Contagem esperada por fixture nos `mustHaveEdges` (fixture-a ≥ 5 BINDS, fixture-b ≥ 5).

---

## Etapa 5 — Decorators como aresta semântica

**Objetivo.** Distinguir decorators de calls normais e ligar o decorator ao alvo decorado.

**Modelo.**
- Adicionar `DecoratesEdge` em `entity/edge.go` (não existe hoje). Alternativa mais leve: manter Call órfão mas incluir `role: "decorator"` + `decorates: <id>` como propriedades. **Recomendo criar o EdgeType** — o modelo já tem `HAS_TYPE`, `IMPLEMENTS`, `EXTENDS` etc. como semântica dedicada; decorator é primeira classe também.

**Arquivos alvo.**
- `entity/edge.go` (novo EdgeType).
- `extractor/parser/javascript/` (visitor de `decorator` nodes).
- `extractor/repository/neo4j/neo4j.go` (adicionar `DecoratesEdge` ao whitelist `allowedEdgeTypes`).

**Passos.**
1. Ao encontrar `@X()` sobre Class/Method/Parameter/Accessor:
   - Emitir `Call` node do próprio `X()` (já existe).
   - Emitir `DECORATES Call → <Class|Function|Parameter alvo>` (na direção decorator → alvo).
   - O `CALLS Call → X` (Class ou Function externa, ex.: `Injectable`) continua igual.
2. Remover o array `decorators: [string]` das propriedades OU manter para debug. Se manter, adicionar comentário no README.

**Fixtures.**
- fixture-a: 8 decorators (@Injectable ×2, @Controller ×1, @Get ×1, @Post ×1, @Param ×1, @Body ×1).

**Aceite.**
- `MATCH (c:Call)-[:DECORATES]->() WHERE c.id STARTS WITH "fixture-a:" RETURN count(*)` ≥ 8.
- Assertions específicas: existe `DECORATES` de `Controller('users')` para a Class `UsersController`; de `Get(':id')` para o método `getUser`; de `Param('id')` para o parâmetro `id`.

---

## Etapa 6 — Superfície HTTP (Endpoint / HttpParam / EXPOSES / HANDLED_BY / HAS_PARAM)

**Objetivo.** Ler decorators NestJS/Express e produzir a superfície de API. Depende da Etapa 5.

**Modelo.**
- Tipos já existem no `entity/`. Só falta emitir.

**Arquivos alvo.**
- Novo `extractor/parser/javascript/http.go` (ou similar) — pattern-matcher em cima da árvore de decorators já resolvida.

**Passos.**
1. Reconhecer classes decoradas com `@Controller(basePath?)` e criar `Endpoint` nodes para cada método com `@Get/@Post/@Put/@Patch/@Delete/@Options/@Head`. Path final = `basePath + methodPath`.
2. Emitir `EXPOSES Application → Endpoint` (ou `File → Endpoint`, decidir).
3. Emitir `HANDLED_BY Endpoint → <method Function>`.
4. Para parâmetros do método com `@Param/@Body/@Query/@Headers`, criar `HttpParam` e emitir `HAS_PARAM Endpoint → HttpParam` + `BINDS HttpParam → <Parameter>`.
5. Guardar `method: "GET"`, `path: "/users/:id"`, `paramKind: "path"|"body"|"query"|"header"` como propriedades.

**Fixtures.**
- fixture-a: 2 endpoints. `GET /users/:id → getUser` com HttpParam path `id`. `POST /users → create` com HttpParam body.

**Aceite.**
- `MATCH (e:Endpoint) WHERE e.id STARTS WITH "fixture-a:" RETURN count(e)` = 2.
- Cada Endpoint tem exatamente 1 `HANDLED_BY`.
- Novo teste `TestFixtureAHttpSurface` valida path e método.

---

## Etapa 7 — Nós de statement (control flow)

**Objetivo.** Modelar `if/else`, `for`, `while`, `switch/case`, `try/catch`, `throw`, `return`. Sem isso, análise de fluxo é impossível.

**Modelo.**
- Tipos já existem: `Assignment`, `Return`, `Throw`, `If`, `Loop`, `Switch`, `Case`, `Try`, `Catch`.
- Edge `NEXT` também existe (sucessor no fluxo) e `FLOWS_TO` (dataflow).

**Arquivos alvo.**
- `extractor/parser/javascript/` — visitor de `statement_block` e derivados.

**Passos.**
1. Emitir nós para cada statement estruturado, contidos pela função pai (`CONTAINS Function → Stmt`).
2. `If`: `NEXT If → thenBlock`, `NEXT If → elseBlock` (se existir). Propriedade `condition: <texto>`.
3. `Loop` (for/while/for-of/for-in): `NEXT Loop → body`. Propriedade `kind: "for" | "while" | "for-of" | "for-in"`.
4. `Try`: `NEXT Try → tryBlock`, `Try → Catch`, `Catch → catchBlock`, opcional `Try → finallyBlock`.
5. `Throw`: propriedade `expr: <texto>`. Aresta `THROWS Function → Throw` (adicionar EdgeType) ou apenas `CONTAINS` — decidir.
6. `Return`: se retorno é identificador, `READS Return → <symbol>`. `NEXT Return → <sentinela de saída>` ou marcar `terminal: true`.
7. `Assignment`: se já capturamos `WRITES`/`READS`, avaliar se vale criar `Assignment` node redundante. Recomendo criar só quando o AST expressa algo que `WRITES` sozinho não captura (ex.: assignment como expressão em `if (x = fetch())`).

**Fixtures.**
- Ampliar fixtures para exercitar todos: `if` em `UsersService.findOne`, `throw`, `return`; `for` em `range()`; `try/catch` — precisa acrescentar um try/catch numa fixture. Talvez estender `client.js` com `try { JSON.parse(raw) } catch { ... }`.

**Aceite.**
- `mustHaveNodes` do teste ganha `If`, `Loop`, `Throw`, `Return`, `Try`, `Catch`.
- Cobertura `emittedNodeTypes` inclui esses tipos.
- Contagem sanity: fixture-a tem ≥ 1 Throw, ≥ 1 If, ≥ 1 Return; fixture-b tem ≥ 1 Loop, ≥ 1 If.

---

## Etapa 8 — Walk de template literals e modificadores restantes

**Objetivo.** Fechar as lacunas menores restantes: reads em template strings, `private/protected/readonly/abstract` em Field/Function, `optional` em Field/Parameter, generics args em HAS_TYPE.

**Arquivos alvo.**
- `extractor/parser/javascript/` — visitor de `template_string`/`template_substitution`, extração de modificadores TS.
- `extractor/linker/javascript/` — HAS_TYPE de argumentos de generic.

**Passos.**
1. **Template literals**: percorrer expressões dentro de `${...}` e emitir `READS` normalmente. Aplicar tanto em fields (`this.prefix`), variáveis, parâmetros, chamadas.
2. **Modificadores TS**: no visitor de field/method definition, ler `accessibility_modifier` (`public|private|protected`), `readonly`, `abstract`, `override`, `static` (já pega) e gravar como booleanos nas properties.
3. **Optional**: `age?: number` — flag `optional: true`.
4. **Generic args**: em `HAS_TYPE`, além de emitir para o construtor genérico (`Map`), emitir uma aresta por argumento (`HAS_TYPE Field → User` com `role: "generic-arg", index: 1`). Alternativa: novo EdgeType `HAS_TYPE_ARG`.
5. **Return type**: em `RETURNS_TYPE`, desembrulhar unions gerando múltiplas arestas quando o membro for um Class/Interface do projeto; ignorar `undefined`/`null` (ou emitir com `nullable: true`).

**Fixtures.**
- Já cobertas. Só validar reads que hoje não aparecem: `BaseLogger.error READS Field prefix`, `AppLogger.log READS Field prefix`, ambas via template literal.

**Aceite.**
- Testes específicos:
  - `BaseLogger.error` tem `READS → BaseLogger.prefix`.
  - `UsersRepository.users` tem HAS_TYPE para `Map` + HAS_TYPE_ARG (ou HAS_TYPE) para `User`.
  - `BaseLogger.log` tem `abstract: true` no Function.
  - `UsersService.logger` tem `readonly: true, private: true`.
  - `UsersRepository.findById` tem RETURNS_TYPE para `User` (mesmo com union `| undefined`).

---

## Etapa 9 — Cosméticos (nome de Call/File, IDs de arrow anônima)

**Objetivo.** Melhorar navegabilidade no Neo4j Browser.

**Arquivos alvo.**
- `extractor/parser/javascript/` — geração de IDs e propriedades.

**Passos.**
1. `Call.name = calleeText` (ou os primeiros 40 chars).
2. `File.name = filepath.Base(path)`.
3. Padronizar ID de função anônima: usar `#fn@<line>:<col>` sempre; o Call que a instancia usa `#<parentId>/call@<line>:<col>` sem sufixo duplicado.

**Aceite.**
- Nenhum `n.name IS NULL` em Calls e Files do grafo.
- Um único nó `Function` por callback anônimo.

---

## Como validar cada etapa

1. `go test ./extractor/... -run TestFixtures` — cobertura por tipo, quebra se um tipo esperado não aparecer.
2. `go test ./extractor/repository/neo4j/... -run TestNeo4jPersistFixtureA` — persistência não regride (contagens distintas).
3. `bash scripts/extract-all.sh` — extração real; queries de sanidade no console.
4. Cypher manual para checagens específicas listadas em cada etapa.

## Ordem de merge sugerida

Blocos independentes que podem ir em PRs separados:

- **PR-1**: Etapa 1 (super + calls em External). Barato, alto valor, sem mudança de modelo.
- **PR-2**: Etapa 2 + Etapa 3 (CommonJS exports + re-export barrels). Barato, sem mudança de modelo.
- **PR-3**: Etapa 4 (BINDS). Requer marcar `BindsEdge` como emitido — ajuste no whitelist do Neo4j e no `emittedEdgeTypes` do teste.
- **PR-4**: Etapas 5 + 6 (Decorators + HTTP surface). Este bloco introduz novo `DecoratesEdge` e ativa `Endpoint/HttpParam/EXPOSES/HANDLED_BY/HAS_PARAM`.
- **PR-5**: Etapa 7 (control flow). Maior custo, revisar fixtures.
- **PR-6**: Etapa 8 (template + modificadores + generics).
- **PR-7**: Etapa 9 (cosméticos).
