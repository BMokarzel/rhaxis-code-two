# Revisão de `entity/node.go`

Análise das structs criadas em `entity/node.go` a partir da interpretação
do documento `tree-sitter-example.md`. O objetivo é verificar se o modelo
de domínio consegue representar todas as construções que apareceriam na
árvore sintática produzida pelo tree-sitter, apontar o que já está
mapeado, o que está incompleto e o que está faltando.

---

## Veredito rápido

O esqueleto **captura a intenção geral** e mapeia boa parte das
construções do documento tree-sitter, mas está em estado ainda muito
inicial: várias structs estão vazias, faltam campos essenciais
(principalmente **corpo / filhos / relacionamentos**), e algumas
construções centrais de qualquer linguagem estão ausentes. Do jeito
atual, seria possível *nomear* nós, mas não **reconstruir** um programa
a partir deles.

---

## 1. O que já mapeia bem (tree-sitter → node.go)

| tree-sitter                                                    | node.go               | estado    |
| -------------------------------------------------------------- | --------------------- | --------- |
| `function_declaration`, `arrow_function`, `method_definition`  | `Function`            | parcial   |
| `call_expression`, `new_expression`                            | `Call`                | parcial   |
| `if_statement`                                                 | `If`                  | parcial   |
| `else_clause`                                                  | `Else`                | parcial   |
| `while_statement`                                              | `While`               | parcial   |
| `try_statement`                                                | `Try`                 | esqueleto |
| `catch_clause`                                                 | `Catch`               | esqueleto |
| `return_statement`                                             | `Return`              | ok        |
| `throw_statement`                                              | `Throw`               | ok        |
| `object` / `class_declaration`                                 | `Object`              | conflate  |
| `identifier` (declaração)                                      | `Variable`, `Constant`| esqueleto |
| literais (`string`, `number`, `template`)                      | `Data`                | genérico  |
| `interface` (TS/Go)                                            | `Interface`           | esqueleto |
| enums                                                          | `Enum`                | esqueleto |

---

## 2. Problemas estruturais (mais importantes)

1. **Não existe conceito de "corpo" / bloco.**
   Em tree-sitter, quase todo nó de controle tem um
   `body: (statement_block ...)`. `If`, `While`, `Function`, `Try`,
   `Catch` não têm nenhum campo apontando para os filhos. Sem isso a
   hierarquia se perde — vira só uma lista plana de nós.

2. **Nenhum modelo de aresta / relacionamento.**
   Um `Call` precisa apontar para a `Function` chamada (ou para um
   símbolo não resolvido). Uma `Function` precisa listar seus
   parâmetros. Um `Object` precisa listar seus campos e métodos. Hoje
   não há como expressar isso — nem por embed, nem por ID, nem por
   slice de filhos.

3. **`Condition string` em `If` e `While` é limitante.**
   Em tree-sitter a condição é uma **subárvore de expressão**
   (`binary_expression`, `member_expression`, `call_expression` etc.).
   Guardando como string, perde-se tudo que vem depois: análise de
   referências dentro da condição, chamadas dentro da condição, etc.
   Deveria ser um `Expression` (ou `Node`).

4. **`Function` e `Call` têm `Location Location` (valor), mas a
   interface `Code` exige `Location() *Location`.**
   Nenhuma das structs implementa a interface `Code` hoje — o campo tem
   o mesmo nome do método esperado, o que também gera conflito quando
   se tentar adicioná-lo depois.

5. **`Node interface{}` vazio é essencialmente `any`.**
   Sem pelo menos `Kind()` / `ID()` não se consegue percorrer a árvore
   de forma polimórfica.

6. **`Object` mistura classe, instância e objeto literal.**
   No tree-sitter esses são nós distintos:
   - `class_declaration` (definição de tipo)
   - `object` (literal `{a: 1}`)
   - `new_expression` (instância em runtime)

   Também não há distinção entre `Object` e `Interface` (contrato) — ok
   em Go, mas para suportar JS/TS/Java é preciso separar.

---

## 3. Construções ausentes (que o próprio doc tree-sitter mostra)

- **Import** / `import_statement` — total ausência. Essencial para
  montar grafo de dependências.
- **Reference / Identifier** — o *uso* de um identificador (diferente
  da declaração). Sem isso, não dá pra rastrear "onde `CONFIG` é lido".
- **MemberAccess** (`obj.prop`) — a construção mais frequente em código
  OO, sem representação.
- **Assignment** e **AugmentedAssignment** (`x = y`, `x += 1`) —
  ausentes.
- **BinaryExpression / UnaryExpression** — operadores.
- **For** (clássico) e **ForEach / ForOf / ForIn** — ausentes; só
  `While` existe.
- **Switch / Case / Break / Continue** — ausentes.
- **Await** e **Yield** — importante já que existe `Async bool`.
- **This / Self** — referência ao objeto corrente.
- **Parameter** — deveria ser um nó próprio, com nome, default e tipo.
- **Field / Property** — atributo de classe/objeto.
- **Method** — hoje mesclado com `Function`; ok se `Function` tiver um
  campo `Receiver *Object` ou similar.
- **Type / TypeReference** — para linguagens tipadas (Go, TS, Java) é
  preciso modelar o tipo declarado.
- **Literal** discriminado (`StringLiteral`, `NumberLiteral`,
  `BoolLiteral`, `NullLiteral`) — `Data` como bag genérico dificulta
  consultas.
- **Array / Tuple literal** — ausente.
- **Template string / Interpolation** — ausente.
- **Module / File** — nenhum contêiner de arquivo. Onde ficam as
  top-level declarations?
- **Scope** — se se quer resolver nomes, é preciso disso (ou derivar da
  hierarquia).
- **Comment / Docstring** — útil para endpoints, exemplos, LLM.

---

## 4. Ambiguidades / decisões que faltam

- `Endpoint` é abstração, ok — mas **precisa** apontar pra `Function`
  handler, senão vira só um label.
- `Repository` e `Application` estão vazios — presume-se que virão
  depois; registrar que eles são o "raiz" do grafo (contêm `File`s /
  `Module`s).
- Diferença semântica entre `Constant` e `Variable` (imutabilidade)
  precisa ser clara — hoje ambas são `struct{}`.
- `Data`, `Object`, `Interface` — a fronteira entre eles não está
  definida. Um objeto literal `{a:1}` é `Object` ou `Data`?
- `Return` e `Throw` precisam apontar pro valor retornado / lançado.

---

## 5. Resumo — o que priorizar antes de continuar

1. Definir um contrato mínimo em `Node` (`Kind()`, `ID()`,
   `Children()`).
2. Introduzir `Block` / `Body` e usar como campo em `Function`, `If`,
   `Else`, `While`, `Try`, `Catch`, `For`.
3. Tratar `Condition` como `Node` (expressão), não `string`.
4. Adicionar os grandes ausentes: `Import`, `Reference`, `MemberAccess`,
   `Assignment`, `BinaryExpression`, `For`, `Parameter`, `Field`,
   `Literal` discriminado, `File` / `Module`.
5. Separar `Class` (definição) de `Object` (literal) e de
   `Instantiation` (`new`).
6. Corrigir o conflito nome-de-campo × método `Location()` da interface
   `Code`.

---

## 6. Próximo passo sugerido

Antes de escrever mais código, montar um **mapa 1:1** entre cada nó
nomeado da grammar tree-sitter que o projeto vai usar (ex.:
`tree-sitter-javascript`, `tree-sitter-go`, `tree-sitter-python`) e o
nome sugerido do struct no domínio. Esse mapa serve como checklist
completo do que precisa existir em `entity/node.go` antes de começar a
implementação do extrator.
