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
