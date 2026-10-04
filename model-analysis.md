# Análise do modelo de grafo — nodes, edges e extração

Retrato de `entity/node.go` + `entity/edge.go` e do que a extração JS/TS em
`extractor/parser/javascript/javascript.go` efetivamente emite, com crítica
ao final.

---

## 1. Nodes — divididos por camada (como estão no código)

| Camada | Nodes | O que são |
|---|---|---|
| **Estrutura** | `Repository`, `Application`, `Module`, `File`, `Package`, `External` | Contêineres físicos/lógicos. Não são AST. `Package` = dependência externa com ID global; `External` = nome não resolvido (ex: `console`, `JSON`). |
| **Declarações** | `Function`, `Parameter`, `Variable`, `Class`, `Interface`, `Field`, `Enum`, `EnumMember` | Símbolos nomeados com identidade estável (ID = nome qualificado). |
| **Código (statements)** | `Call`, `Assignment`, `Return`, `Throw`, `If`, `Loop`, `Switch`, `Case`, `Try`, `Catch`, `Jump` | Atos/trechos sem nome próprio. ID = posição (linha:col). Efêmeros: morrem na re-extração. |
| **Contrato HTTP** | `Endpoint`, `HttpParam` | Abstração de rota + parâmetros OpenAPI. Não vêm da AST, vêm de regras (`@Controller`, `@Get`, `@Param`). |
| **Autoria** | `Commit`, `Person`, `Team` | IDs globais, sobrevivem à re-extração da app. |
| **Cross-service** | `Topic`, `LinkReview` | Mensageria + revisão humana para resoluções ambíguas. |

`Base` dá só `ID`. `CodeBase` adiciona `Location{FileID,Start,End}` +
`OwnerID` (a Function/File que o contém). Todo node de código embute
`CodeBase`.

---

## 2. Edges — por família

| Família | Edges | Observação |
|---|---|---|
| **Estrutura** | `CONTAINS`, `DECLARES`, `HAS_PARAMETER`, `HAS_FIELD`, `HAS_METHOD`, `HAS_MEMBER`, `EXPORTS` | Vem direto da extração de 1 arquivo. |
| **Uso (expressões achatadas)** | `READS`, `WRITES` (c/ `Operator`), `ARGUMENT` (c/ `Index`) | *Substituem* o node `Reference` da proposta antiga. |
| **Resolução** | `IMPORTS`, `CALLS`, `INSTANTIATES`, `HAS_TYPE`, `HAS_TYPE_ARG`, `RETURNS_TYPE`, `EXTENDS`, `IMPLEMENTS` | O linker preenche carregando `Resolution`. |
| **Fluxo (derivadas)** | `INVOKES`, `FLOWS_TO`, `NEXT`, `THROWS` | `NEXT` já é emitido no `walkBlock`. `INVOKES`/`FLOWS_TO` só declarados — ainda não vi serem produzidos. |
| **Contrato HTTP** | `EXPOSES`, `HANDLED_BY`, `HAS_PARAM`, `BINDS`, `REQUESTS` | `REQUESTS` cobre tanto `Call → Endpoint` quanto `Call → Application` (R1/R2). |
| **Decorator** | `DECORATES` | `Call → Class/Function/Parameter/Field`. Elegante: usa o próprio `Call` do decorator sem criar um node novo. |
| **Autoria** | `AUTHORED_BY`, `COMMITTED_BY`, `CHANGED`, `MEMBER_OF`, `OWNS`, `CREATED`, `LAST_MODIFIED` | |
| **Cross-service** | `PRODUCES`, `CONSUMES` | `Function → Topic`. |

---

## 3. `CONTAINS` × `DECLARES` — a pergunta central

**Mesmo `From` (File ou Function). Destino diferente:**

| Edge | Destino | Semântica | Estabilidade do ID do destino |
|---|---|---|---|
| `DECLARES` | **Declarações nomeadas**: `Function`, `Class`, `Interface`, `Enum`, `Variable` | "nasce aqui um símbolo cujo nome entra num escopo" | **estável** (nome qualificado) |
| `CONTAINS` | **Statements/expressões sem nome**: `Call`, `Assignment`, `Return`, `Throw`, `Try`, `Catch`, `Switch`, `Case`, `If`, `Loop`, `Jump` | "neste owner acontece esse ato" | **efêmero** (posição) |

Além disso:

- `CONTAINS` também é usado em nível macro: `Application → File` (no
  `linker.go:148`).
- `DECLARES` nunca aparece para statements; `CONTAINS` nunca para símbolos
  nomeados.

Por que a separação importa:

- Edges vindas de fora (`CALLS c → fn`) só podem apontar para alvos
  **estáveis**. Daí símbolos (DECLARES) terem ID por nome.
- Edges internas a um arquivo podem apontar para destinos **efêmeros**.
  Daí statements (CONTAINS) poderem ter ID por posição e serem
  reconstruídos a cada re-extração.

**Quem contém o quê, concretamente** (olhando `walk` do parser JS):

```
Application  --CONTAINS-->  File

File  --DECLARES-->  Function | Class | Interface | Enum | Variable (top-level)
File  --CONTAINS-->  Call | Assignment | If | Loop | Return | Throw | Try | ...   (statements em top-level)
File  --EXPORTS-->  qualquer declaração

Function  --DECLARES-->  Function aninhada | Class | Variable local
Function  --CONTAINS-->  Call | If | Loop | Return | Throw | Try | Catch | Switch | Case | Assignment | Jump
Function  --HAS_PARAMETER{index}-->  Parameter

Class  --HAS_FIELD-->  Field
Class  --HAS_METHOD-->  Function
Class  --EXTENDS-->  Class
Class  --IMPLEMENTS-->  Interface

Interface  --HAS_FIELD-->  Field
Interface  --HAS_METHOD-->  Function
Interface  --EXTENDS-->  Interface

Enum  --HAS_MEMBER-->  EnumMember

Endpoint  --HANDLED_BY-->  Function
Endpoint  --HAS_PARAM-->  HttpParam
HttpParam  --BINDS-->  Parameter
```

---

## 4. Expressões achatadas — o que *não* existe

A proposta original (`graph-model-review.md`) tinha nodes `Reference`,
`MemberAccess`, `Operation`, `Literal`, `ObjectLiteral`. O modelo final
**removeu todos** e trocou por:

- `READS owner → variavel/field/parameter` com propriedade `member`
  (ex: `user.email` → READS para `user` com `member="email"`).
- `WRITES owner → alvo` com `Operator` (`=`, `+=`, `++`).
- `ARGUMENT{index} Call → portador`.
- `calleeText` armazenado no próprio `Call`.

Esta é a decisão arquitetural mais importante do modelo: trocou **nodes de
uso** por **edges de uso**. Resultado: muito menos nodes (que seriam a
maioria), mas perde-se a capacidade de perguntar "qual é o node da
*expressão da condição* do if?". Hoje você sabe que um `If` existe e que
o owner lê `x`, mas não sabe se `x` é lida *dentro da condição* ou
*dentro do body* sem reabrir o AST.

---

## 5. Crítica — o que faz sentido, o que não faz, o que juntar/remover

### 5.1 O que está bem resolvido

- **Fusões via `Kind`** (`Function`, `Loop`, `Variable`, `Call`, `Jump`):
  seguiram a recomendação do review. Boa decisão — menos tipos, mesma
  informação.
- **`Package` vs `External`**: separação justificada. Package tem ID
  global para `PROVIDED_BY`/`REQUESTS` cross-service; External é só
  placeholder.
- **IDs estáveis para declarações** (`<file>#<qual>`) + IDs efêmeros para
  statements. Correto para re-extração incremental.
- **`DECORATES` reaproveita `Call`** em vez de criar node `Decorator`.
  Minimalismo bom.
- **`Resolution` em edges**: `exact | inferred | name_only | ambiguous`.
  Essencial para o algoritmo de impacto distinguir fato de palpite.
- **`LinkReview` modelado como escalares paralelos** (sem list of maps)
  para casar com Neo4j — pragmático.

### 5.2 O que está divergindo da documentação (comentários desatualizados)

- O comentário de `ContainsEdge` diz "Application → File; File/Function →
  Call", mas na prática também sai para
  `Return/Throw/Try/Catch/Switch/Case/If/Loop/Jump/Assignment`.
  **Atualizar o comentário**.
- `ReadsEdge` comenta `"Function/File/Call → portador"`, mas também sai
  de `Return`, `Throw`, `Assignment` (ver `simpleStmt`/`assignmentNode`).
  **Atualizar**.
- `WritesEdge` comenta `"Function/File → portador"`, mas sai de
  `Assignment` (quem reencaminha com `c.with`). **Atualizar**.

### 5.3 O que parece não fazer sentido no estado atual

- **`ModuleNode` existe mas não é emitido em lugar nenhum visível** no
  fluxo JS. Ou é implementado em breve ou vira *dead code*. **Candidato
  a remoção** até ter uso real.
- **`InvokesEdge` e `FlowsToEdge` estão declaradas mas não emitidas**
  (não há derivação). Hoje são promessa, não grafo. **Ou implementa a
  fase de derivação, ou tira do catálogo** para não enganar quem
  consulta.
- **`CommittedByEdge`** só emitida "quando committer ≠ author" — ótimo,
  mas vale checar se o pipeline realmente respeita isso ou duplica.
  Risco baixo, mas revisar.

### 5.4 Fronteiras duvidosas

- **`CONTAINS` serve para dois níveis muito distintos**: `Application →
  File` (hierarquia física) e `Function → Call` (statement dentro do
  corpo). Semanticamente coerente ("mora dentro"), mas numa travessia
  `MATCH (f:Function)-[:CONTAINS*]->(x)` isso pode saltar fronteiras se
  não cuidar. Vale uma `kind` property discriminando `structural` vs
  `body`, ou duas edges distintas (`CONTAINS` x `HAS_STATEMENT`). Pelo
  princípio "um tipo de edge por papel" da seção §5 do review,
  provavelmente o correto é **dividir**: `CONTAINS` para
  file-system/estrutura macro, `HAS_STATEMENT` (ou reaproveitar nome
  como `BODY`/`STATEMENT`) para o papel sintático.
- **`FileScopeID` e `OwnerID` são redundantes com `CONTAINS`/`DECLARES`**.
  Denormalização aceitável para lookup O(1) de "em que função moro?",
  mas qualquer código que os altere precisa manter os dois consistentes.
  Vale documentar que `OwnerID` é fonte da verdade e `CONTAINS`/`DECLARES`
  são derivadas (ou vice-versa).
- **`Call` tem `Name` + `CalleeText` com o mesmo valor**
  (`Name: calleeText, ... CalleeText: calleeText` em `call()`). Um dos
  dois é redundante — provavelmente `Name` existe só para "navegação no
  Neo4j Browser", como o comentário diz. Aceitável, mas é um alerta: ao
  renomear, cuidar dos dois.

### 5.5 Candidatos a juntar

- **`EXPOSES` + `HANDLED_BY`**: hoje são duas edges para o mesmo
  endpoint (`File → Endpoint` e `Endpoint → Function`). Faz sentido
  manter — perguntas diferentes (quem expõe vs quem trata). **Manter.**
- **`CallsEdge` com destino polimórfico**
  (`Function | External | Package`): está certo, mas isso torna consultas
  de "quais chamadas resolveram para externo" dependentes de olhar o
  *label* do destino. Alternativa: ter `CallsExternalEdge` separado.
  Prejudica mais do que ajuda — **manter como está**.
- **`REQUESTS` com dois sentidos** (`Call → Endpoint` R1,
  `Call → Application` R2): está documentado mas o consumidor precisa
  discriminar via label do destino. Semelhante a `CALLS`. Aceitável.

### 5.6 Possíveis remoções

- `ModuleNode` (não usado).
- `InvokesEdge`, `FlowsToEdge` (declaradas sem produtor) — ou
  implementar, ou remover.
- A redundância `Call.Name` × `Call.CalleeText` pode ir embora se o
  Neo4j Browser aceitar `calleeText` como display.

### 5.7 Gap conhecido do modelo

O achatamento de expressões em `READS/WRITES/ARGUMENT` custa:

- Não há como distinguir "`x` lido na condição do `if`" de "`x` lido no
  body". A edge `READS` sai do owner, não do `If`.
- Não há "operand index" de `BinaryExpression`.
- A pergunta "quais `if`s dependem de `CONFIG.retries`?" do review
  (§8.3) **não é respondível** sem reabrir o AST.

Esse foi um trade-off consciente (volume >>> granularidade sintática),
mas vale deixar explícito no README/docs: *o modelo não preserva
estrutura de expressão; preserva fluxo e identidade de símbolo*.

---

## TL;DR

- **`CONTAINS`** = "mora em" (Application→File + Function→statement sem
  nome).
- **`DECLARES`** = "nasce aqui um símbolo nomeado"
  (File/Function → Function/Class/Variable/Interface/Enum).
- **Declarações** = símbolos com ID estável; **statements** = atos com
  ID posicional.
- O modelo trocou nodes de uso (Reference/MemberAccess) por edges
  (`READS/WRITES/ARGUMENT`) — menos volume, menos granularidade
  sintática.
- Pontos a limpar: `ModuleNode`, `InvokesEdge`, `FlowsToEdge` sem
  produtor; comentários desatualizados em `CONTAINS`/`READS`/`WRITES`;
  sobrecarga semântica do `CONTAINS` em dois níveis distintos.
