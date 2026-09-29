# Exemplo de extração via tree-sitter (JavaScript)

Este arquivo mostra um pequeno arquivo JavaScript e a árvore sintática
concreta (CST) produzida pelo `tree-sitter-javascript`, no formato
S-expression (o mesmo que `tree-sitter parse` imprime no terminal).

O objetivo é servir de referência visual para ver **como cada
construção aparece na árvore**: declaração de funções, chamadas,
controle de fluxo, declaração de classes/objetos, referências a
identificadores, imports, etc.

Convenções da notação S-expression do tree-sitter:

- `(node_type)` — um nó da árvore.
- `field: (child)` — nome de campo (definido na grammar) apontando para um filho.
- Nós **nomeados** aparecem entre parênteses; tokens **anônimos** (como `{`, `=>`, `if`) só existem como filhos anônimos e normalmente são omitidos ao imprimir.
- Cada nó tem `start_point` / `end_point` (linha, coluna) — omitidos aqui para caber na página.

---

## 1. Código-fonte de exemplo (`sample.js`)

```javascript
// 1. import  ---------------------------------------------------
import { readFile } from "node:fs/promises";

// 2. constante e objeto literal --------------------------------
const CONFIG = {
  path: "./data.json",
  retries: 3,
};

// 3. função declarada ------------------------------------------
function greet(name) {
  return `hello, ${name}`;
}

// 4. função como arrow / expressão -----------------------------
const double = (n) => n * 2;

// 5. classe (declaração de "objeto") ---------------------------
class Counter {
  constructor(start = 0) {
    this.value = start;
  }

  inc() {
    this.value += 1;
    return this.value;
  }
}

// 6. função async com controle de fluxo ------------------------
async function loadUsers() {
  const c = new Counter();          // instanciação (chamada com `new`)
  let raw;

  try {
    raw = await readFile(CONFIG.path, "utf8");   // chamada de função
  } catch (err) {
    if (err.code === "ENOENT") {                 // if / else
      return [];
    } else {
      throw err;
    }
  }

  const users = JSON.parse(raw);                 // referência a objeto global

  for (const u of users) {                       // for-of loop
    c.inc();                                     // method call
    console.log(greet(u.name));                  // chamada aninhada
  }

  while (c.value < CONFIG.retries) {             // while loop
    c.inc();
  }

  return users.map(double);                      // função passada como valor
}

loadUsers();                                     // chamada top-level
```

---

## 2. Árvore S-expression produzida por tree-sitter

Reproduz aproximadamente a saída de:

```
tree-sitter parse sample.js
```

(Comentários `// …` foram inseridos por mim para anotar; a saída real
do tree-sitter é puramente estrutural.)

```scheme
(program

  ;; ---------- (1) import ------------------------------------------------
  (import_statement
    (import_clause
      (named_imports
        (import_specifier
          name: (identifier))))                 ;; readFile
    source: (string
      (string_fragment)))                       ;; "node:fs/promises"

  ;; ---------- (2) const + object literal --------------------------------
  (lexical_declaration                          ;; palavra-chave: const/let
    (variable_declarator
      name: (identifier)                        ;; CONFIG
      value: (object
        (pair
          key:   (property_identifier)          ;; path
          value: (string (string_fragment)))
        (pair
          key:   (property_identifier)          ;; retries
          value: (number)))))                   ;; 3

  ;; ---------- (3) function declaration ----------------------------------
  (function_declaration
    name: (identifier)                          ;; greet
    parameters: (formal_parameters
      (identifier))                             ;; name
    body: (statement_block
      (return_statement
        (template_string
          (template_substitution
            (identifier))))))                   ;; ${name}

  ;; ---------- (4) arrow function atribuída a const ---------------------
  (lexical_declaration
    (variable_declarator
      name: (identifier)                        ;; double
      value: (arrow_function
        parameters: (formal_parameters
          (identifier))                         ;; n
        body: (binary_expression
          left:  (identifier)                   ;; n
          right: (number)))))                   ;; 2

  ;; ---------- (5) classe ------------------------------------------------
  (class_declaration
    name: (identifier)                          ;; Counter
    body: (class_body

      (method_definition
        name: (property_identifier)             ;; constructor
        parameters: (formal_parameters
          (assignment_pattern
            left:  (identifier)                 ;; start
            right: (number)))                   ;; 0
        body: (statement_block
          (expression_statement
            (assignment_expression
              left:  (member_expression
                object:   (this)
                property: (property_identifier)) ;; value
              right: (identifier)))))            ;; start

      (method_definition
        name: (property_identifier)             ;; inc
        parameters: (formal_parameters)
        body: (statement_block
          (expression_statement
            (augmented_assignment_expression
              left:  (member_expression
                object:   (this)
                property: (property_identifier)) ;; value
              right: (number)))                  ;; 1
          (return_statement
            (member_expression
              object:   (this)
              property: (property_identifier))))))) ;; value

  ;; ---------- (6) async function + controle de fluxo -------------------
  (function_declaration
    name: (identifier)                          ;; loadUsers
    parameters: (formal_parameters)
    body: (statement_block

      ;; const c = new Counter();
      (lexical_declaration
        (variable_declarator
          name: (identifier)                    ;; c
          value: (new_expression
            constructor: (identifier)           ;; Counter
            arguments:   (arguments))))

      ;; let raw;
      (lexical_declaration
        (variable_declarator
          name: (identifier)))                  ;; raw

      ;; try / catch
      (try_statement
        body: (statement_block
          (expression_statement
            (assignment_expression
              left:  (identifier)               ;; raw
              right: (await_expression
                (call_expression
                  function:  (identifier)       ;; readFile
                  arguments: (arguments
                    (member_expression
                      object:   (identifier)    ;; CONFIG
                      property: (property_identifier)) ;; path
                    (string (string_fragment)))))))) ;; "utf8"
        handler: (catch_clause
          parameter: (identifier)               ;; err
          body: (statement_block
            (if_statement
              condition: (parenthesized_expression
                (binary_expression
                  left:  (member_expression
                    object:   (identifier)      ;; err
                    property: (property_identifier)) ;; code
                  right: (string (string_fragment)))) ;; "ENOENT"
              consequence: (statement_block
                (return_statement
                  (array)))                     ;; []
              alternative: (else_clause
                (statement_block
                  (throw_statement
                    (identifier))))))))         ;; err

      ;; const users = JSON.parse(raw);
      (lexical_declaration
        (variable_declarator
          name: (identifier)                    ;; users
          value: (call_expression
            function: (member_expression
              object:   (identifier)            ;; JSON
              property: (property_identifier))  ;; parse
            arguments: (arguments
              (identifier)))))                  ;; raw

      ;; for (const u of users) { ... }
      (for_in_statement                         ;; nome do nó cobre for-of também
        kind: "const"
        left:  (identifier)                     ;; u
        right: (identifier)                     ;; users
        body: (statement_block
          (expression_statement
            (call_expression
              function: (member_expression
                object:   (identifier)          ;; c
                property: (property_identifier)) ;; inc
              arguments: (arguments)))
          (expression_statement
            (call_expression
              function: (member_expression
                object:   (identifier)          ;; console
                property: (property_identifier)) ;; log
              arguments: (arguments
                (call_expression
                  function:  (identifier)       ;; greet
                  arguments: (arguments
                    (member_expression
                      object:   (identifier)    ;; u
                      property: (property_identifier))))))))) ;; name

      ;; while (c.value < CONFIG.retries) { ... }
      (while_statement
        condition: (parenthesized_expression
          (binary_expression
            left:  (member_expression
              object:   (identifier)            ;; c
              property: (property_identifier))  ;; value
            right: (member_expression
              object:   (identifier)            ;; CONFIG
              property: (property_identifier)))) ;; retries
        body: (statement_block
          (expression_statement
            (call_expression
              function: (member_expression
                object:   (identifier)          ;; c
                property: (property_identifier)) ;; inc
              arguments: (arguments)))))

      ;; return users.map(double);
      (return_statement
        (call_expression
          function: (member_expression
            object:   (identifier)              ;; users
            property: (property_identifier))    ;; map
          arguments: (arguments
            (identifier))))))                   ;; double

  ;; ---------- (7) chamada top-level -------------------------------------
  (expression_statement
    (call_expression
      function:  (identifier)                   ;; loadUsers
      arguments: (arguments))))
```

---

## 3. Mapa rápido: construção → tipo(s) de nó tree-sitter

| Construção no código                         | Nó(s) tree-sitter                                                            |
| -------------------------------------------- | ---------------------------------------------------------------------------- |
| Import                                       | `import_statement` → `import_clause` → `named_imports` / `import_specifier`  |
| `const`/`let`/`var`                          | `lexical_declaration` / `variable_declaration` + `variable_declarator`       |
| Objeto literal `{ k: v }`                    | `object` → `pair` (`key:` + `value:`)                                        |
| Identificador simples (referência)           | `identifier`                                                                 |
| Propriedade de objeto (referência)           | `member_expression` (`object:` + `property:` como `property_identifier`)     |
| Função nomeada                               | `function_declaration` (`name:`, `parameters:`, `body:`)                     |
| Arrow function                               | `arrow_function` (`parameters:`, `body:`)                                    |
| Método de classe                             | `method_definition` dentro de `class_body`                                   |
| Declaração de classe                         | `class_declaration` (`name:`, `body: class_body`)                            |
| `new Foo(...)`                               | `new_expression` (`constructor:`, `arguments:`)                              |
| Chamada `f(x)` / `obj.m(x)`                  | `call_expression` (`function:`, `arguments: (arguments ...)`)                |
| Atribuição `a = b`                           | `assignment_expression` (`left:`, `right:`)                                  |
| Atribuição composta `a += b`                 | `augmented_assignment_expression`                                            |
| Operador binário `a < b`, `a * b`, `a === b` | `binary_expression` (`left:`, `right:`, operador é filho anônimo)            |
| `if / else`                                  | `if_statement` (`condition:`, `consequence:`, `alternative: else_clause`)    |
| `for (... of ...)` / `for (... in ...)`      | `for_in_statement`                                                           |
| `for (i=0; i<n; i++)`                        | `for_statement`                                                              |
| `while`                                      | `while_statement` (`condition:`, `body:`)                                    |
| `try / catch / finally`                      | `try_statement` (`body:`, `handler: catch_clause`, `finalizer:`)             |
| `throw`                                      | `throw_statement`                                                            |
| `return`                                     | `return_statement`                                                           |
| `await x`                                    | `await_expression`                                                           |
| `this`                                       | `this`                                                                       |
| String literal                               | `string` → `string_fragment` (o texto real)                                  |
| Template literal `` `... ${x} ...` ``        | `template_string` → `template_substitution`                                  |
| Número                                       | `number`                                                                     |
| Array literal `[]`                           | `array`                                                                      |

---

## 4. Como reproduzir esta saída

```bash
# instalar
npm i -g tree-sitter-cli
git clone https://github.com/tree-sitter/tree-sitter-javascript
cd tree-sitter-javascript

# gerar e testar
tree-sitter generate
tree-sitter parse caminho/para/sample.js
```

Para extrair nós específicos (por exemplo, todas as chamadas de função) use
uma **query** `.scm` com o mecanismo de queries do tree-sitter:

```scheme
;; captura toda call_expression e o nome sendo chamado
(call_expression
  function: [
    (identifier) @callee
    (member_expression property: (property_identifier) @callee)
  ]) @call

;; captura declarações de função e o nome
(function_declaration name: (identifier) @func.name) @func.def

;; captura referências a identificadores (uso, não declaração)
((identifier) @ref
 (#not-match? @ref "^(this|undefined)$"))

;; captura métodos de classe
(class_declaration
  name: (identifier) @class.name
  body: (class_body
    (method_definition
      name: (property_identifier) @method.name) @method.def)) @class.def
```

Rodar com:

```bash
tree-sitter query queries.scm sample.js
```

O output lista cada captura (`@callee`, `@func.name`, ...) com sua posição
(linha:coluna início → linha:coluna fim) e o texto correspondente — que
é normalmente a forma como se **extrai** informação de um projeto usando
tree-sitter (indexação, análise estática leve, geração de grafos de
símbolos, etc.).
