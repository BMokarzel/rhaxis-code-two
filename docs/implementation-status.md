# Status de implementação dos gaps

Auditoria feita em 2026-10-01, cruzando `docs/extraction-gaps-plan.md` com o código.

## Já implementado antes desta onda

| Item | Local | Observação |
|---|---|---|
| Resolução de `super` | `extractor/linker/linker.go:426-434` + `linker.go:568` | emite `CALLS Call → <ParentClass.method>` |
| Chamadas em field com tipo External | `linker.go:567-569` | `this.map.get()` → `CALLS Call → Map` com `Member` |
| CommonJS `module.exports` | `parser/javascript/javascript.go:500-580` (`commonjsExport`) | cobre `module.exports = X`, `= {...}`, `exports.x = …` |
| Re-export barrels | `linker.go:300-341` | `export * from` e `export { X } from` |
| `BINDS` granular | `linker.go:286-296` | inclui External nomeado sob Package (ex.: `pkg:node:fs/readFile`) |
| `DECORATES` edge | `parser:1185` | Call → Class/Function/Parameter/Field |
| HTTP surface | `parser:723,851,866` | `EXPOSES`, `HANDLED_BY`, `HAS_PARAM`, `HttpParam`, `Endpoint` |
| Statement nodes básicos | `parser:206-232,1377-1446` | `If`, `Loop`, `Switch`, `Case`, `Try`, `Catch`, `Return`, `Throw` |
| Cosméticos (nomes de Call/File) | `parser:76,1375+` | `File.Name` via `filepath.Base`; `Call.Name` = CalleeText |

## Onda 1 (2026-10-01)

| Item | Mudanças |
|---|---|
| `ThrowsEdge` | Novo `EdgeType` em `entity/edge.go`; emitido em `parser.simpleStmt` quando o nó é `Throw` e owner é Function; whitelist do Neo4j atualizada |
| `HasTypeArgEdge` | Novo `EdgeType` em `entity/edge.go`; emitido em `typeRef` (generic) e `inferNew`; roteado para `linkType` no linker, preservando `Index`; **não** registra `typeOf` para não quebrar resolução de membro no container |
| `JumpNode` (break/continue) | Novo `NodeType`+`Jump` struct em `entity/node.go`; removido de `skipKind`; handler `jumpStmt` emite nó + `CONTAINS` (sem fixture exercitando ainda) |
| `typeRefIndexed` | Refactor interno para permitir `Index` em `PendingRef` de tipos |
| Teste de integração | `emittedEdgeTypes` ganhou `HasTypeArgEdge` e `ThrowsEdge` |

Resultado em fixture-a: `THROWS=1`, `HAS_TYPE_ARG=1` (User em `Map<string, User>`). Todos os testes verdes.

## Onda 2 (2026-10-01)

| Item | Mudanças |
|---|---|
| `AssignmentNode` extraído | `assignment_expression`, `augmented_assignment_expression`, `update_expression` passam a emitir `Assignment{Operator}`; `CONTAINS Function → Assignment`; `WRITES Assignment → LHS`; `READS/CALLS/INSTANTIATES Assignment → RHS`. Preserva `Call.OwnerID = Function` para não quebrar `deriveInvokes` nem `CONTAINS Function → Call`. |
| `write` passa a usar `c.from` | Antes: `WRITES` saía fixo de `c.owner`. Agora: sai de `c.from`, que o `assignmentNode` injeta como o Assignment. Scope default continua `from == owner`, então var-decls, params e for-of sem Assignment ficam idênticos. |
| `Return`/`Throw` ligados ao valor | `return_statement` e `throw_statement` agora reemitem o walk dos filhos com `c.from = <Return/Throw>.id`, gerando `READS Return → x`, `CALLS Return → <call>`, etc. (antes esses edges saíam da Function, perdendo a ligação). |
| Teste `TestWritesAndArguments` | Ajustado para percorrer `CONTAINS Counter.inc → Assignment -WRITES{+=}-> Counter.value` (antes assumia a edge direta Function→Field). |
| Teste de integração | `AssignmentNode` entrou em `emittedNodeTypes` e em `fixture-b.mustHaveNodes`. |

Resultado em fixture-b: `Assignment=4`, `WRITES=16`, `READS=41`. Pré-requisito para `FLOWS_TO` derivado — a origem do dado agora está no grafo.

## Onda 3 (2026-10-01)

| Item | Mudanças |
|---|---|
| `NEXT` edges entre statements | Novo helper `walkBlock` (`javascript.go:1491`) usado em `statement_block`, corpo de função `{...}` e nível de arquivo (`program`). Para cada filho, snapshot em `x.fg.Edges` antes do walk; a primeira `CONTAINS` com `From == c.owner` adicionada identifica o node de statement. Se há `prev`, emite `NEXT prev → current`. |
| `firstContainedStmt` | Helper puro que varre o slice de edges a partir do snapshot. Pulado: declarações puras (var decls sem efeito colateral), type aliases, comentários — o `NEXT` então pula para o próximo statement com node. |
| Teste de integração | `NextEdge` adicionado a `emittedEdgeTypes` e ao `mustHaveEdges` de `fixture-b`. |

Resultado: fixture-a emite 9 `NEXT`, fixture-b emite 19. Ordem textual (named children) determina a sequência. Todos os testes verdes.

Dependência da heurística: cada helper de statement (`simpleStmt`, `ifNode`, `loopNode`, `caseNode`, `jumpStmt`, `assignmentNode`, `call`) deve emitir **primeiro** o node e **em seguida** `CONTAINS owner → id` antes de descer na subárvore. Se um novo helper for adicionado emitindo subelementos antes da própria CONTAINS, `walkBlock` escolherá o node errado.

## Onda 4 (2026-10-01)

| Item | Mudanças |
|---|---|
| `deriveFlowsTo` | Novo passo no linker (`linker.go:429`) rodado depois de `deriveInvokes`. Varre `r.graph.Edges` uma vez indexando READS/WRITES por Assignment, READS por Return/Throw, ARGUMENT por Call, CALLS → Function e HAS_PARAMETER por Function. |
| Regra 1 (Assignment) | Produto cartesiano `Assignment.reads × Assignment.writes` emite `R -FLOWS_TO-> W`. Cobre `x = y`, `x = foo()` (Call → Var), `this.f = y`, destructuring parcial, augmented (`+=`, `++`). |
| Regra 2 (Return) | `R -FLOWS_TO-> Return` por read + `Return -FLOWS_TO-> Function(owner)` para fechar o inter-procedural via Call do caller. |
| Regra 3 (Throw) | `R -FLOWS_TO-> Throw` por read. Não liga Throw à Function porque semanticamente o valor sai pela borda de exceção. |
| Regra 4 (Argumento → Parâmetro) | `Call -ARGUMENT{i}-> X` + `Call -CALLS-> F` + `F -HAS_PARAMETER-> P{Index=i}` emite `X -FLOWS_TO-> P`. Só quando o alvo é Function node (CallsEdge resolvido para FunctionNode). |
| Teste de integração | `FlowsToEdge` em `emittedEdgeTypes` e `fixture-b.mustHaveEdges`. |

Resultado: fixture-a emite 24 FLOWS_TO, fixture-b emite 28. Dedup é automático via `addEdge` (FLOWS_TO sempre sai sem Loc). Não emite self-loops.

Com isso, fica no grafo a cadeia completa:
```
Call -FLOWS_TO-> Variable  (via Assignment)
Variable -FLOWS_TO-> Parameter  (via Argument)
Parameter -FLOWS_TO-> Return  (via assignments internas e return)
Return -FLOWS_TO-> Function  (fim do intra-procedural)
Function → Caller.Assignment via CALLS + outra Assignment  (chainagem inter-procedural)
```

## Onda 5 (2026-10-01)

| Item | Mudanças |
|---|---|
| `variable_declarator` com init embrulhado em Assignment | `variables()` (`javascript.go:1004-1057`) agora cria um `Assignment` para todo declarator com RHS (menos arrow/function/class literal, que seguem o fast-path existente). WRITES Assignment → cada identificador declarado (incluindo destructuring); walk do RHS vai sob o scope do Assignment. |
| Destructuring com link ao fonte | Como consequência do item acima: `const {a, b} = obj` passa a emitir `Assignment -READS-> obj` + `Assignment -WRITES-> a` + `Assignment -WRITES-> b`. `deriveFlowsTo` então produz `obj -FLOWS_TO-> a` e `obj -FLOWS_TO-> b`. Mesmo para `const [x, y] = arr`. Member/Index específico do key ainda não é preservado — o fluxo é grosso ("obj flui para a"), não fino ("obj.a flui para a"). |
| `const x = foo()` idem | O fast-path para arrow/class continua, mas qualquer `const x = <expr>` (chamada, soma, identifier) agora cria Assignment, fechando o loop `foo() -FLOWS_TO-> x` que antes se perdia. |
| Helper de teste `hasReachableEdge` | `javascript_test.go:50`: aceita a aresta direta ou um salto via `CONTAINS`. Semântica: "a função X tem READS/WRITES para Y, direta ou via statement contido". `TestNestSample` passa a usar esse helper para READS/WRITES (antes esperava edge direto Function→Enum). |

Resultado em fixture-b: `Assignment=16` (antes 4), `READS=50` (antes 41), `FLOWS_TO=37` (antes 28). O ganho vem da promoção de `const x = <expr>` a Assignment, que até agora emitia só WRITES direto Function→Var e perdia a origem.

Trade-off aceito: o grafo ganhou nós intermediários (Assignment), o que aumenta a contagem total mas abre a navegação por fluxo. Testes de função direta precisam andar via `CONTAINS` — formalizado no helper `hasReachableEdge`.

## Onda 6 (2026-10-01)

| Item | Mudanças |
|---|---|
| INVOKES via ARGUMENT para Function | `deriveInvokes` (`linker.go:390`) ganha segundo braço: qualquer `Call -ARGUMENT-> Function` emite `owner -INVOKES-> arg` com `Resolution=Inferred`. Cobre Promise `.then/.catch/.finally`, `arr.map/.forEach/.filter/.reduce`, `addEventListener`, `register(h)`, qualquer HOF que receba função nomeada por posição. Trade-off: falso-positivo para APIs que só armazenam o callback (ex.: event bus), mas desbloqueia a maioria dos usos reais. |
| Fixture exercitando Jump | `testdata/fixture-b-cli/src/lib/utils.js` ganhou `continue` + `break` em `range`. Fixture-b agora emite 2 `JumpNode`s. `JumpNode` entrou em `emittedNodeTypes` e em `fixture-b.mustHaveNodes` — a cobertura automática do tipo deixou de depender só da emissão silenciosa. |
| Template literal reads | **Já funcionavam** (default walk cobre `template_substitution`). Item estava documentado como gap no doc antigo, mas investigação com probe test confirmou que `${this.prefix}` emite READS para `this.prefix` e `${name}` para `name`. Removido da lista de pendências. |

Resultado em fixture-b: `INVOKES=8` (antes 7), `Jump=2` (nova cobertura). Todos os testes verdes.

## Onda 7 (2026-10-01)

| Item | Mudanças |
|---|---|
| `PendingRef.Member` | Novo campo em `ir/ir.go`. Carregado só pelos WRITES de destructuring; nos demais refs o Member sai da resolução do linker (path). `linkUse` usa `ref.Member` como fallback quando o path resolvido não produziu membro. |
| `patternBindings` helper | Novo em `javascript.go:1808`. Devolve `[]patternBinding{Ident, Member}` percorrendo `object_pattern`/`array_pattern`/`pair_pattern`/`rest_pattern`. Para `{a}` → Member="a"; `{b: c}` → Member="b"; `[x,y]` → "0","1"; `[, y]` → "1". Posição em array vem do número de vírgulas consumidas (não do número de named children). |
| `variables()` + `write()` object/array_pattern | Trocaram `patternIdentifiers` por `patternBindings` e passam `Member` para o WRITES. `patternIdentifiers` continua existente — ainda é usado em places onde só interessa a lista de identificadores (loops `for...of` com `kind`, decoradores, etc.). |
| `deriveFlowsTo` Regra 1 | Index de writes passou a ser `[]writeTarget{id, member}`. O FLOWS_TO emitido copia `Member` do WRITES — resultado: `const {a} = obj` emite `obj -FLOWS_TO-> a {Member: "a"}` em vez da edge grossa anterior. |
| Teste `TestDestructuringMember` | Inline (fixture temporária com `os.WriteFile`), porque todas as fixtures reais usam destructuring só em `require(...)` — que é curto-circuitado em `requireImport` antes de chegar no fluxo de Assignment. Valida que `{a, b: aliased}` + `[x, y]` produz WRITES e FLOWS_TO com os quatro Members esperados ("a", "b", "0", "1"). |

Resultado em fixture-b: contagens de `Assignment=7` (parser) / `Assignment=16` (linker) e `FLOWS_TO=37` inalteradas — os valores vindos de destructuring nas fixtures existentes passam todos por `requireImport`, que não exercita o novo caminho. Cobertura real fica no teste focado.

## Sprint G1 — Git source (2026-10-02)

Primeira sprint do `docs/next-wave-plan.md`. Nenhuma mudança no parser/linker — só nova
fonte de arquivos.

| Item | Mudanças |
|---|---|
| `extractor/source/git/` | Novo pacote. `Provider` implementa `source.Provider` lendo de uma revisão sem checkout. Lista blobs via `git -C <repo> ls-tree -r -z --full-tree <rev> [-- <subdir>]` e lê conteúdo em lote via **um** processo `git cat-file --batch` (SHAs no stdin, cabeçalho + conteúdo + `\n` no stdout). `File.Hash` passa a ser o SHA do blob (40 hex); `File.Path` é relativo ao `Subdir`. |
| Filtros | Mesmos de `source/local`: ignora `node_modules|dist|build|coverage|vendor`, diretórios ocultos, `.d.ts`, extensões fora do registry. Arquivos com nome começando por "." **não** são filtrados (bate com `local`). |
| CLI `-rev` e `-subdir` | `extractor/cmd/main.go` ganhou duas flags. Sem `-rev` continua usando `source/local` (comportamento atual). Com `-rev <sha|branch|tag>` lê via git; `-subdir services/x` restringe ao subdir (bom para monorepo). `key` default vira `basename(subdir)` quando houver subdir. |
| `buildSource` helper | Centraliza a escolha entre `local` e `gitsrc`. `runMemory`/`runNeo4j` passam a receber uma `source.Provider` já construída (sem conhecer o tipo). |
| Testes | `TestGitSourceSelectsRevision` (2 commits, troca de conteúdo entre revisões), `TestGitSourceSubdir` (restrição por subdir), `TestGitSourceEquivalenceWithLocal` (`HEAD` produz mesmo conjunto de paths/content que `source/local`; só `Hash` muda de formato), `TestGitSourceReadFile` (ReadFile lê blobs arbitrários e falha corretamente em revisões sem o arquivo). Todos saltam com `t.Skip` se `git` não estiver no PATH. |

Dependência externa: `git` CLI no PATH. Nenhum módulo Go novo.

## Sprint G2 — Autoria mínima (2026-10-02)

Segunda sprint do `docs/next-wave-plan.md`. Sem blame ainda — isso é Sprint G3.

| Item | Mudanças |
|---|---|
| `entity.Commit`, `entity.Person`, `entity.Team` | Novos nodes com IDs **globais** (não prefixados por `<appKey>:`), portanto sobrevivem a reextrações da app. `CommitNode`, `PersonNode`, `TeamNode` adicionados aos NodeType. |
| Edges de autoria | `AuthoredByEdge`, `CommittedByEdge`, `ChangedEdge`, `MemberOfEdge`, `OwnsEdge`, `CreatedEdge`, `LastModifiedEdge` em `entity/edge.go`. Sprint G2 só emite as três primeiras; o restante é reservado para G3. |
| `Edge.Kind` | Novo campo opcional. Usado por `CHANGED` com `added|modified|deleted|renamed|copied|type_changed|touched`. Será reaproveitado por `OWNS` (`blame|codeowners|config`) no Sprint G3. Neo4j persiste como property `kind`. |
| Whitelist Neo4j | `allowedEdgeTypes` em `extractor/repository/neo4j/neo4j.go` ganha os 7 novos tipos. `edgeProps` serializa `Kind`. |
| `extractor.Enricher` | Nova interface + `Enrichers []Enricher` em `Extractor`. Roda **depois** do linker e **antes** da persistência. Mutação in-place no `*entity.Graph`. Usada por authorship e, no futuro, cross-service. |
| `extractor/authorship/` | Novo pacote com `config.go` (schema + loader de `.rhaxis.yaml`), `person.go` (normalização de email + ID determinístico + hash sha256 opcional), `commit.go` (parse de `git log --use-mailmap --pretty=format:'%H%x1F...'` + `git diff-tree -r -z -M -C --root` com separadores NUL), `authorship.go` (orquestração + adapter `Enricher`). |
| CHANGED (Commit → File) | Para cada commit, emite uma edge por arquivo tocado, com `Kind` traduzido do status A/M/D/R/C/T. **Só** emite se o File já existe no grafo (evita edges para arquivos binários/removidos/ignorados). `RenamedFrom` reaproveita o campo `Member`. |
| CLI | Flags novas em `extractor/cmd/main.go`: `-config`, `-authorship`/`-no-authorship`, `-store-email=plain|hash|none`, `-commits-range`, `-commits-limit`. `.rhaxis.yaml` opcional; flags sobrescrevem. |
| go.mod | `gopkg.in/yaml.v3` v3.0.1 agora é dependência (indireta por não haver `_` import, mas usada). |
| Testes | `TestEnrichDisabledIsNoop`, `TestEnrichCommitPersonChanged` (repo de 3 commits, 2 autores, deleção de arquivo), `TestEnrichStoreEmailHash`/`None`, `TestLoadFileMissing`/`YAML`, `TestConfigValidateRejectsInvalid`. Todos saltam se `git` não estiver no PATH. |

**Decisão explícita:** CHANGED aponta para File (não para declaração) nesta sprint. Dar granularidade por declaração sem blame geraria overestimate (toda declaração do arquivo tocado seria marcada). A precisão por declaração entra no Sprint G3 via `CREATED` e `LAST_MODIFIED` (que usam blame).

**Decisão explícita:** CommitNode e PersonNode são globais (não prefixados por `<appKey>:`). Isso significa que `deleteApp` do Neo4j não os remove ao reextrair a app — mesmo Commit pode ser referenciado por múltiplas apps do mesmo repo. CHANGED edges são deletadas em cascata quando a File é deletada e recriadas na próxima extração + authorship.

## Sprint G3 — Blame por declaração (2026-10-02)

Terceira sprint. Escopo reduzido para apenas Person (sem Teams/CODEOWNERS, conforme ajuste de escopo do usuário em 2026-10-02).

| Item | Mudanças |
|---|---|
| `Edge.Share` | Novo campo opcional `float64` em `entity/edge.go`. Usado por `OWNS` como fração de linhas atribuídas (0..1). `edgeProps` do Neo4j serializa quando `> 0`. |
| `extractor/authorship/blame.go` | `runBlame` executa `git blame --porcelain -w -M -C <rev> -- <path>`. `parseBlame` entende o formato porcelain (header SHA, bloco de metadata só na primeira ocorrência de cada commit, linha de conteúdo com TAB como terminador). Cache por SHA para não repetir alocação por linha. `--use-mailmap` foi omitido porque git ≤2.34 não aceita a flag no `blame`; mailmap continua opt-in via `blame.mailmap=true` no repo. |
| `extractor/authorship/ownership.go` | `isOwnableDeclaration` filtra Function/Class/Interface/Field/Enum/EnumMember + Variable top-level (Owner termina em `.ts/.js/.tsx/.jsx`). `collectDecls` agrupa por File com `span` ascendente para `innermost` escolher a declaração mais interna que contém a linha. `deriveOwnership` roda blame uma vez por File do grafo, acumula `declStats` (autores, min/max author-time) e emite `OWNS` (ordenado por personID para determinismo), `CREATED` (minPerson), `LAST_MODIFIED` (maxPerson). |
| `OWNS.Kind="blame"` + `Share` | Toda edge de `OWNS` desta sprint tem `Kind="blame"` e `Share = autor_lines / total_lines`. Soma dos shares por declaração ≈ 1. |
| Integração | `Enrich` em `authorship.go` chama `deriveOwnership` depois dos CHANGED. Blame de arquivo untracked/removido vira log em stderr (`authorship: blame <path>: <err>`) e segue; não derruba a extração. |
| Testes | `TestOwnershipBlameEmitsOwnsCreatedLastModified` (repo de 3 commits de 2 autores em `svc.ts`, Function cobrindo 6 linhas; valida Share total ≈ 1 e CREATED/LAST_MODIFIED → autor correto), `TestOwnershipSkipsFilesWithoutDeclarations` (File sem declaração não gera OWNS), `TestOwnershipBlameMissingFileIsSoftError` (File fantasma só loga em stderr), `TestOwnershipWithSubdir` (blame recebe `Subdir + "/" + path`). |

**Decisão explícita:** Variable só é "ownable" quando é top-level de um arquivo JS/TS (owner é o File). Variáveis locais a funções/blocos herdariam `OWNS` da função, duplicando atribuição e inflando o grafo.

**Decisão explícita:** `innermost` escolhe a declaração de **menor span** que contém a linha. Isso significa que uma linha dentro de um método de uma classe atribui `OWNS` para o método, não para a classe. Para ter autoria da classe inteira seria preciso somar OWNS dos métodos + fields em query — decidido ficar fora desta sprint.

**Decisão explícita:** Teams, CODEOWNERS e `.rhaxis-teams.yaml` ficam fora do produto por enquanto. `MemberOfEdge` segue na whitelist do Neo4j mas sem emissão. Reavaliar quando houver usuário real pedindo rollup por time.

## Sprint G4 — Signals + cross-service URL/env (2026-10-02)

Quarta sprint. Link cross-service por **inferência de sinais** — sem exigir arquivo de configuração entre serviços. Granularidade de v1 é App → App; refinamento para Call → Endpoint fica como follow-up quando literais de argumento virarem nodes do grafo.

| Item | Mudanças |
|---|---|
| `entity.Topic`, `entity.LinkReview` | Novos nodes. Topic tem ID global `topic:<broker>:<name>` para convergir PRODUCES/CONSUMES de apps diferentes no mesmo node (Sprint 5). LinkReview tem ID escopado `<appKey>:review:<sha1[:12]>`, portanto sumido no re-extract da app, exceto reviews com Status=confirmed/overridden/rejected (preservados pelo repositório). |
| `entity.Application` ganhou sinais | `Endpoints []string` (formato "METHOD PATH"), `EnvVarsRead []string`, `HostCandidates []string`. Todos opcionais; serializam como arrays de strings — suportado nativamente por Neo4j. |
| `entity.LinkReview.Candidate*` | Candidatos são `[]string`+`[]int`+`[]string` paralelos (`CandidateTargets`, `CandidateScores`, `CandidateWhys`) porque Neo4j não aceita list of maps como property. Len deve ser igual nos três. |
| Edges novas | `ProducesEdge` e `ConsumesEdge` em `entity/edge.go` (reservadas pra Sprint 5). `RequestsEdge` já existia e foi reaproveitada. Whitelist Neo4j atualizada. |
| `ResolutionAmbiguous` | Nova constante Resolution. Marca REQUESTS quando há empate forte no topo dos candidatos — a edge aponta pro top mas há `LinkReview` pendente. |
| `extractor/crosservice/signals.go` | `SignalsEnricher` (implementa `extractor.Enricher`). Varre o grafo e popula a Application: Endpoints (dos nodes `Endpoint`), EnvVarsRead (das edges `READS → External{name=process}` com `Member` começando com `env.` — pega só o primeiro componente antes do próximo ponto). HostCandidates é extraído por regex (`urlLiteralRE`) direto do conteúdo dos arquivos via `source.Provider`. Limites de corte: top 50 endpoints / 20 envs / 20 hosts para não inchar a Application. |
| `extractor/crosservice/url_resolver.go` | R1. Para cada host da `src`, pontua apps alvo: +2 se o alvo também lista o host, +2 se o `nameMatch` entre host head e Key/Name do alvo bate. `nameMatch` normaliza underscore/hífen e aceita prefixos `name-...` / `name_...`. |
| `extractor/crosservice/env_resolver.go` | R2. Para cada env lida pela `src`, pontua: +2 se o alvo lê a mesma env, senão +1 se o prefixo (até o primeiro `_`) casa com Key/Name do alvo. Prefixo não existente → score 0. |
| `extractor/crosservice/linker.go` | `Link(apps)` roda R1+R2 para todas as apps. Regra de resolução: 1 candidato → `exact` se score top>=2, senão `inferred`; 2+ com empate no topo → top vira edge `ambiguous` + LinkReview pendente; 0 candidatos → silêncio (incrementa `Unmatched`). ID do LinkReview é `sha1(src|kind|hint)[:12]`, portanto reruns reaplicam sem duplicar. |
| `extractor/repository/neo4j/query.go` | Repository ganhou `LoadApplications`, `ReplaceCrossServiceLinks`, `ListPendingReviews`, `SetReviewStatus`, `UpdateRequestsTarget`, `DeleteRequestsEdge`, `AuditUnlinkedExternals`. `ReplaceCrossServiceLinks` apaga REQUESTS/PRODUCES/CONSUMES da app src + LinkReview com Status=pending (preserva confirmed/overridden/rejected) antes de inserir o novo conjunto; portanto idempotente. |
| CLI reescrito pra subcomandos | `extractor/cmd/main.go` passa a despachar subcomando (default = `extract` pra compat). Novo `link-services` com sub-sub-comandos: `link` (padrão), `review`, `confirm <reviewID>`, `override <reviewID> <targetID>`, `reject <reviewID>`, `audit`. `audit` lista Call → External HTTP (fetch/axios/request/got/http) sem REQUESTS saindo da app. Tudo via Cypher no Neo4j. |
| Signals wired em todo `extract` | `buildEnrichers` sempre inclui `SignalsEnricher` (recebe o `source.Provider`). Sem custo quando o grafo não tem endpoints / env vars / URLs. |
| Testes | 5 testes de signals (endpoints, env vars, host candidates via `local.New` + tempdir, no-src skip, auto-attach), 6 testes do linker (exact by name, ambíguo com review, no match → unmatched, env prefix inferred, self-link ignorado, review ID determinístico). Todos passando. |

**Decisão explícita:** Granularidade de v1 do REQUESTS é App → App, não Call → Endpoint. Fazer Call → Endpoint hoje exigiria materializar argumentos de string como nodes do grafo (hoje são texto solto no `CalleeText`/arg index). Esse refinamento entra quando um usuário real precisar — ou quando Sprint 5 introduzir literais nomeados.

**Decisão explícita:** HostCandidates é extraído por **regex sobre o texto do arquivo**, não por traversal do grafo. O trade-off: 2x I/O (parser + regex), mas evita mudar o parser para emitir literais e evita que a precisão de inferência dependa de resolve de concatenação de strings. A regex é restrita a `(https?|wss?)://host[:port]`, portanto não casa `foo:bar` sem protocolo.

**Decisão explícita:** `link-services` só roda com Neo4j (`NEO4J_URI` obrigatório). O modo in-memory extrai uma app por vez — não há "banco de apps" pra ligar. A denormalização de sinais na Application funciona em ambos os modos; só o cross-link requer Neo4j.

**Decisão explícita:** Resolver nunca prompta durante `extract`. Sempre que há ambiguidade, cria um `LinkReview` que o humano revisa depois com `review`/`confirm`/`override`/`reject`. Isso mantém `extract` 100% automatizável em CI.

**Fora do escopo desta sprint:** Sprint 5 (R3 — PRODUCES/CONSUMES via tópicos Kafka/RabbitMQ/SQS), R4 — SDK alias (depende de library extraction).

## Sprint G5 — Topics PRODUCES/CONSUMES (2026-10-02)

Quinta sprint. R3 do plano cross-service. Detecta chamadas de mensageria no código JS/TS e liga a `Function` que contém a chamada a um `Topic` global. Apps diferentes convergem no mesmo `Topic` (`topic:<broker>:<name>`), e é por essa convergência — não por edge direta — que o grafo mostra producer → consumer.

| Item | Mudanças |
|---|---|
| `extractor/crosservice/topics.go` | `TopicsEnricher{Src}` implementa `extractor.Enricher`. Para cada file do source, lê o conteúdo via `source.Provider.ReadFile`, roda duas regex: `positionalRE` casa `<callee>.<method>('T', ...)` com aspa simples/dupla/backtick; `objectRE` casa `<callee>.<method>({topic\|QueueUrl\|TopicArn: 'name', ...})`. Line number vem de `bytes.Count(data[:match], '\n') + 1`. |
| Mapeamento broker | `brokerFromCallee` por prefixo do callee: `kafka*`/`producer`/`consumer` → kafka; `rabbit*`/`channel` → rabbitmq; `sqs*` → sqs; `sns*` → sns; `redis*` → redis; `pubsub*` → pubsub. Prefixo desconhecido → `"unknown"` (ainda emite Topic). |
| Normalização de nome | `normalizeTopicName` strippa prefixo de URL do SQS (`https://sqs.us-east-1.amazonaws.com/123/my-queue` → `my-queue`) e de ARN do SNS (`arn:aws:sns:us-east-1:123:my-topic` → `my-topic`). Para outros brokers devolve raw. |
| Mapeamento call → Function | `collectFunctionsByFile` indexa todas as `Function` do grafo por FileID com span ascendente; `innermostFn` escolhe a Function de menor span que contém a linha do hit — reusa o padrão de `authorship/ownership.go`. Hit fora de qualquer função é silenciosamente ignorado. |
| Emissão | Para cada hit mapeado: cria `entity.Topic{Name, Broker}` com ID global se ainda não existir (dedupe por `topicsSeen`), e adiciona `entity.Edge{Type: PRODUCES\|CONSUMES, From: fn.ID, To: topic.ID, Resolution: inferred}`. `CONSUMES` quando o método é `consume\|subscribe\|receiveMessage\|psubscribe`; o resto é `PRODUCES`. |
| Wire-up | `extractor/cmd/main.go` em `buildEnrichers` adiciona `crosservice.TopicsEnricher{Src: src}` logo depois do `SignalsEnricher`. Roda sempre; no-op se `Src` for nil (modo in-memory sem source não entra aqui, mas o guard é barato). |
| Testes | 7 testes em `topics_test.go`: PRODUCES de kafka.send, CONSUMES de .subscribe, object-style sendMessage com SQS QueueUrl (strip), chamada fora de qualquer Function não emite, dois grafos convergem no mesmo `topic:kafka:shared` com PRODUCES/CONSUMES separados, Topic pré-existente não é duplicado, Src nil é no-op. Todos passando. |

**Decisão explícita:** detecção por regex sobre o conteúdo do arquivo, igual R1 (HostCandidates). Mesmo trade-off: 2x I/O vs. mudar o parser para materializar argumentos literais como nodes. O ganho é não acoplar a sprint ao parser e cobrir patterns de SDK que o AST normalmente não desembrulha (`sqs.sendMessage({QueueUrl: '...'})`).

**Decisão explícita:** Topic ID é global (`topic:<broker>:<name>`), não escopado por app. Esse é o único caminho pelo qual dois `extract` independentes conversam entre si via mensageria no Neo4j — queries `MATCH (a)-[:PRODUCES]->(t)<-[:CONSUMES]-(b)` descobrem o pipe sem precisar de link direto App→App. Como `deleteApp` só apaga nodes com prefixo `<appKey>:`, Topics sobrevivem a re-extracts.

**Decisão explícita:** quando um callee tem prefixo desconhecido, o Topic ainda é emitido com `broker="unknown"`. A alternativa (filtrar) silenciaria uso real de mensageria por SDKs/wrappers não listados. O humano pode re-rotular depois; o grafo mantém o sinal.

**Fora do escopo desta sprint:** R4 (SDK alias / library extraction), merge de topics com nomes semelhantes (`orders.created` vs `orders.create`), detecção de headers de mensagem / schemas, mapping para Call-level ao invés de Function-level.

## Pendente (ordenado por valor)

### Crítico (destrava leitura de função)

_Nenhum item restante nesta categoria._

### Médio

- **Union/intersection types** não desembrulhados em HAS_TYPE (fica só em `TypeName` texto).
- **Getters/setters** sem inferência de tipo de retorno/parâmetro.

### Baixo

- JSDoc / comentários (hoje descartados).
- Métricas por função (LOC, ciclomática, aninhamento).
- `Telemetry` node para logs/traces/spans.
- Literais mágicos (URLs, SQL, env keys).
