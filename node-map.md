# Mapa 1:1 — nós tree-sitter → structs de domínio

Checklist de todos os nós nomeados relevantes das grammars
`tree-sitter-javascript`, `tree-sitter-typescript`, `tree-sitter-python`
e `tree-sitter-go`, com o nome sugerido do struct em `entity/node.go` e
os campos mínimos que cada um precisa expor.

> Convenção da coluna **Campos sugeridos**:
> - `nome Tipo` — campo escalar
> - `nome []Tipo` — coleção de filhos
> - `nome *Tipo` — referência opcional
> - `Body []Node` — bloco de statements

---

## 1. Raiz do grafo (contêineres, não são nós tree-sitter puros)

| Domínio          | Papel                                                          | Campos sugeridos                                            |
| ---------------- | -------------------------------------------------------------- | ----------------------------------------------------------- |
| `Repository`     | Origem física (github/bitbucket/local)                         | `ID`, `URL`, `Applications []Application`                   |
| `Application`    | Projeto executável, contém módulos/arquivos                    | `ID`, `Name`, `Files []File`, `Endpoints []Endpoint`        |
| `File` / `Module`| Um arquivo-fonte, raiz da AST                                  | `ID`, `Path`, `Language`, `Imports []Import`, `Body []Node` |
| `Endpoint`       | Abstração de rota HTTP/RPC (não vem do tree-sitter)            | `ID`, `Method`, `Path`, `Handler *Function`                 |
| `Scope`          | Escopo léxico (opcional; pode-se derivar da hierarquia)        | `ID`, `Parent *Scope`, `Symbols map[string]Node`            |
| `Location`       | Posição no arquivo                                             | `File *File`, `StartLine`, `StartCol`, `EndLine`, `EndCol`  |

---

## 2. Imports e declarações top-level

| tree-sitter                                                    | Domínio         | Campos sugeridos                                                    |
| -------------------------------------------------------------- | --------------- | ------------------------------------------------------------------- |
| `import_statement` (JS/TS/Py), `import_declaration` (Go)       | `Import`        | `Source string`, `Specifiers []ImportSpecifier`, `Location`         |
| `import_specifier`, `namespace_import`, `import_from_statement`| `ImportSpecifier` | `Local string`, `Imported string`, `Kind` (default/named/namespace) |
| `export_statement`, `export_clause`                            | `Export`        | `Target Node`, `Name string`, `IsDefault bool`                      |

---

## 3. Declarações de valor

| tree-sitter                                                              | Domínio       | Campos sugeridos                                                        |
| ------------------------------------------------------------------------ | ------------- | ----------------------------------------------------------------------- |
| `lexical_declaration` + `variable_declarator` (`let`, JS/TS)             | `Variable`    | `Name`, `Type *TypeRef`, `Init Node`, `Location`                        |
| `lexical_declaration` (`const`, JS/TS), `const_spec` (Go)                | `Constant`    | `Name`, `Type *TypeRef`, `Value Node`, `Location`                       |
| `assignment` (Py), `variable_declaration` (Go)                           | `Variable`    | idem                                                                    |
| `parameter`, `formal_parameters`, `required_parameter`                   | `Parameter`   | `Name`, `Type *TypeRef`, `Default Node`, `Variadic bool`                |
| `field_declaration` (Go), `public_field_definition` (TS), `assignment` em corpo de classe (Py) | `Field` | `Name`, `Type *TypeRef`, `Default Node`, `Owner *Class`  |

---

## 4. Funções e métodos

| tree-sitter                                                       | Domínio        | Campos sugeridos                                                                   |
| ----------------------------------------------------------------- | -------------- | ---------------------------------------------------------------------------------- |
| `function_declaration`, `function_definition` (Py)                | `Function`     | `Name`, `Params []Parameter`, `ReturnType *TypeRef`, `Async bool`, `Body []Node`   |
| `arrow_function`, `function_expression`                            | `Function`     | idem, com `Anonymous bool`                                                         |
| `method_definition` (JS/TS), `method_declaration` (Go)             | `Method`       | embed `Function`, `Receiver *TypeRef` ou `Owner *Class`                            |
| `generator_function` (JS), `yield_expression`                      | `Function` / `Yield` | `Function.IsGenerator bool`; `Yield.Value Node`                              |
| `constructor` (JS/TS via `method_definition[name=constructor]`)    | `Constructor`  | embed `Method`                                                                     |
| `decorator` (Py/TS)                                                | `Decorator`    | `Target Node`, `Expression Node`                                                   |

---

## 5. Tipos, classes, interfaces, enums

| tree-sitter                                                       | Domínio        | Campos sugeridos                                                     |
| ----------------------------------------------------------------- | -------------- | -------------------------------------------------------------------- |
| `class_declaration`, `class_definition` (Py)                      | `Class`        | `Name`, `Extends *TypeRef`, `Implements []TypeRef`, `Body []Node`    |
| `interface_declaration` (TS), `type_spec` com interface (Go)      | `Interface`    | `Name`, `Extends []TypeRef`, `Members []Node`                        |
| `type_alias_declaration` (TS), `type_spec` (Go)                   | `TypeAlias`    | `Name`, `Target TypeRef`                                             |
| `enum_declaration` (TS), `const` em bloco enumerado (Go)          | `Enum`         | `Name`, `Members []EnumMember`                                       |
| `enum_body`, `enum_assignment`                                    | `EnumMember`   | `Name`, `Value Node`                                                 |
| `type_annotation`, `type_identifier`, `generic_type`              | `TypeRef`      | `Name`, `Generics []TypeRef`, `Nullable bool`                        |

---

## 6. Objetos, instâncias e literais estruturados

| tree-sitter                                                     | Domínio          | Campos sugeridos                                              |
| --------------------------------------------------------------- | ---------------- | ------------------------------------------------------------- |
| `object` (literal `{a:1}`), `dictionary` (Py), `composite_literal` (Go) | `ObjectLiteral` | `Pairs []Pair`                                        |
| `pair`, `keyword_argument`                                      | `Pair`           | `Key Node`, `Value Node`                                      |
| `array` (JS), `list` (Py), `composite_literal` (Go, slice)      | `ArrayLiteral`   | `Elements []Node`                                             |
| `tuple` (Py)                                                    | `TupleLiteral`   | `Elements []Node`                                             |
| `new_expression` (JS/TS), chamada de `Foo()` como construtor    | `Instantiation`  | `Constructor Node`, `Arguments []Node`, `Type *TypeRef`       |

---

## 7. Referências e acesso

| tree-sitter                                                     | Domínio          | Campos sugeridos                                              |
| --------------------------------------------------------------- | ---------------- | ------------------------------------------------------------- |
| `identifier` (uso, não declaração)                              | `Reference`      | `Name`, `Resolved Node`, `Location`                           |
| `member_expression`, `attribute` (Py), `selector_expression` (Go) | `MemberAccess` | `Object Node`, `Property string`, `Computed bool`             |
| `subscript_expression`, `index_expression`                      | `IndexAccess`    | `Object Node`, `Index Node`                                   |
| `this`, `super`, `self` (Py)                                    | `SelfRef`        | `Kind` (this/super/self)                                      |
| `spread_element`, `unpacking`                                   | `Spread`         | `Argument Node`                                               |

---

## 8. Chamadas e operadores

| tree-sitter                                                     | Domínio             | Campos sugeridos                                          |
| --------------------------------------------------------------- | ------------------- | --------------------------------------------------------- |
| `call_expression`, `call` (Py)                                  | `Call`              | `Callee Node`, `Arguments []Node`, `Async bool`           |
| `await_expression`                                              | `Await`             | `Value Node`                                              |
| `assignment_expression`, `assignment` (Py)                      | `Assignment`        | `Target Node`, `Value Node`, `Operator string`            |
| `augmented_assignment_expression`, `augmented_assignment`       | `Assignment`        | idem, com `Operator` != `=`                               |
| `binary_expression`                                             | `BinaryExpression`  | `Left Node`, `Right Node`, `Operator string`              |
| `unary_expression`                                              | `UnaryExpression`   | `Operand Node`, `Operator string`, `Prefix bool`          |
| `update_expression` (`i++`)                                     | `UpdateExpression`  | `Operand Node`, `Operator string`, `Prefix bool`          |
| `ternary_expression`, `conditional_expression`                  | `Ternary`           | `Condition Node`, `Then Node`, `Else Node`                |
| `logical_expression`, `boolean_operator` (Py)                   | `BinaryExpression`  | Operator `&&`, `\|\|`, `and`, `or`                        |

---

## 9. Controle de fluxo

| tree-sitter                                                     | Domínio             | Campos sugeridos                                                         |
| --------------------------------------------------------------- | ------------------- | ------------------------------------------------------------------------ |
| `if_statement`, `if_expression` (Py match)                      | `If`                | `Condition Node`, `Then []Node`, `Else *Else`                            |
| `else_clause`, `else` de Python                                 | `Else`              | `Body []Node`, `NextIf *If` (para `else if`)                             |
| `while_statement`                                               | `While`             | `Condition Node`, `Body []Node`                                          |
| `do_statement`                                                  | `DoWhile`           | `Condition Node`, `Body []Node`                                          |
| `for_statement` (clássico C-like)                               | `For`               | `Init Node`, `Condition Node`, `Update Node`, `Body []Node`              |
| `for_in_statement`, `for_of_statement`, `for_statement` (Go/Py) | `ForEach`           | `Iterator Node`, `Iterable Node`, `Body []Node`                          |
| `switch_statement`, `match_statement` (Py 3.10+)                | `Switch`            | `Discriminant Node`, `Cases []Case`                                      |
| `switch_case`, `case_clause`, `case_pattern`                    | `Case`              | `Match Node`, `Body []Node`, `IsDefault bool`                            |
| `break_statement`                                               | `Break`             | `Label string`                                                           |
| `continue_statement`                                            | `Continue`          | `Label string`                                                           |
| `return_statement`                                              | `Return`            | `Value Node`                                                             |
| `throw_statement`, `raise_statement` (Py)                       | `Throw`             | `Value Node`                                                             |
| `try_statement`                                                 | `Try`               | `Body []Node`, `Catches []Catch`, `Finally []Node`                       |
| `catch_clause`, `except_clause` (Py)                            | `Catch`             | `Parameter *Parameter`, `Type *TypeRef`, `Body []Node`                   |
| `finally_clause`                                                | `Finally`           | `Body []Node`                                                            |
| `with_statement` (Py), `defer_statement` (Go)                   | `With` / `Defer`    | `Resource Node`, `Body []Node`                                           |
| `go_statement` (Go), `select_statement` (Go)                    | `Go` / `Select`     | `Call Node`; `Cases []Case`                                              |
| `labeled_statement`                                             | `Label`             | `Name string`, `Statement Node`                                          |

---

## 10. Literais primitivos

| tree-sitter                                                     | Domínio          | Campos sugeridos                                    |
| --------------------------------------------------------------- | ---------------- | --------------------------------------------------- |
| `string`, `string_fragment`                                     | `StringLiteral`  | `Value string`, `Raw string`                        |
| `template_string`                                               | `TemplateString` | `Parts []Node` (fragments + substitutions)          |
| `template_substitution`                                         | `Interpolation`  | `Expression Node`                                   |
| `number`, `integer`, `float`                                    | `NumberLiteral`  | `Value float64` / `Raw string`, `Kind`              |
| `true`, `false`                                                 | `BoolLiteral`    | `Value bool`                                        |
| `null`, `undefined`, `None` (Py), `nil` (Go)                    | `NullLiteral`    | `Kind string`                                       |
| `regex`                                                         | `RegexLiteral`   | `Pattern string`, `Flags string`                    |

---

## 11. Comentários e metadados

| tree-sitter                                                     | Domínio          | Campos sugeridos                                    |
| --------------------------------------------------------------- | ---------------- | --------------------------------------------------- |
| `comment`, `line_comment`, `block_comment`                      | `Comment`        | `Text string`, `Kind` (line/block/doc)              |
| Docstring (Py: primeira string do corpo)                        | `Docstring`      | `Text string`, `Target Node`                        |
| `hash_bang_line`                                                | `Shebang`        | `Value string`                                      |

---

## 12. Contrato mínimo do `Node`

Todo struct listado acima deveria implementar:

```go
type Node interface {
    ID() string           // identificador estável (hash de path+range, por ex.)
    Kind() string         // "Function", "Call", "If", ...
    Location() *Location  // posição no arquivo
    Children() []Node     // filhos diretos para travessia
}
```

E `Code` (a interface que já existe) faria sentido como um alias / marker
para nós que têm origem em código-fonte real (excluindo `Repository`,
`Application`, `Endpoint`, `Scope`).

---

## 13. Como usar este mapa

1. Percorra a tabela e marque quais nós o projeto vai **de fato**
   suportar na v1 (provavelmente Python + JS/TS cobrem a maior parte).
2. Cada linha marcada vira uma `struct` em `entity/node.go`.
3. Rode `tree-sitter parse` em um arquivo real e confira se todos os
   nós que aparecem na saída têm equivalente na sua camada de domínio.
   Nós sem equivalente = gap a preencher (ou decisão consciente de
   ignorar, ex.: `hash_bang_line`).
4. Escreva um extractor por linguagem que lê a árvore tree-sitter e
   emite os structs do domínio — o mapa 1:1 vira o "case switch" desse
   extractor.
