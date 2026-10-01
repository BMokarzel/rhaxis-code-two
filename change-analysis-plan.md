# Plano: extração e análise de mudanças entre commits

Objetivo: dado um intervalo de commits da `main` (local ou vinda do GitHub), responder:

1. **O que mudou**: quais declarações (Function, Class, Field...) foram adicionadas, removidas ou
   alteradas, e de que forma (corpo, assinatura, dependências).
2. **Quais regiões foram afetadas**: quem depende do que mudou (impacto para frente).
3. **Qual mudança pode ter causado um bug**: dado o node onde o sintoma aparece, quais mudanças
   recentes conseguem alcançá-lo (busca de causa para trás).
4. **Histórico e autoria**: como cada declaração era em qualquer commit, e quem a criou, alterou e
   conhece (etapa 11).

O plano reaproveita o pipeline atual (`source → parser → linker → repository`). A extração de um
commit antigo não exige um extrator novo, só uma nova origem do código.

> **Pré-requisito:** fechar a validação do M1 (`docs/architecture.md`). Comparar grafos de um extrator
> que ainda não foi compilado e testado produz diferenças falsas.

---

## Visão geral

```
                git fetch origin main
                         │
         ┌───────────────┴───────────────┐
   source/git @base                source/git @head
         │                               │
   parser (cache por blob)        parser (cache por blob)
         │                               │
      linker                          linker
         │                               │
    grafo base ────────┐   ┌────────── grafo head
                       ▼   ▼
   git diff -U0 -M ──► changeset ──► diff semântico (nodes + edges)
                                         │
                       ┌─────────────────┼──────────────────┐
                       ▼                 ▼                  ▼
                  relatório         impacto (↑)       suspeitos (↓)
                  de mudança        quem depende      a partir do sintoma
                                         │
                                histórico + autoria no Neo4j (etapa 11)
```

O que já existe e torna isso viável:

- **IDs estáveis** para declarações: `<appKey>:<path>#Classe.metodo`. O mesmo método tem o mesmo ID
  nos dois commits, então o diff é por chave e não por heurística.
- **`Location`** em todo node de código, o que permite mapear linhas do `git diff` para nodes.
- **`source.Provider`** é uma interface, então ler de uma revisão do git é só outra implementação.
- **`File.Hash`** já existe e pode passar a ser o SHA do blob, que serve de chave de cache.

---

## Etapa 1: `source/git`, extrair o código de um commit

**Objetivo.** Rodar o extrator sobre qualquer revisão sem fazer checkout no diretório de trabalho.

**Arquivos alvo.**
- `extractor/source/git/git.go` (novo)
- `extractor/cmd/main.go` (flag `-rev`)

**Opções.**

| Opção | Prós | Contras |
|---|---|---|
| **A. Executar o binário `git`** (`ls-tree`, `cat-file --batch`) | Sem dependência nova, rápido, suporta tudo do git | Exige `git` instalado |
| B. Biblioteca `go-git` | Puro Go | Dependência grande; nesta máquina o proxy do Go falha com TLS |
| C. `git worktree add` + `source/local` | Zero código novo de leitura | Escreve no disco, lento para muitos commits, precisa limpar depois |

**Recomendação: A.** A opção C serve como atalho para testar a ideia antes da etapa 1 ficar pronta.

**Passos.**
1. `Walk`: `git ls-tree -r -z --full-tree <rev>` lista `mode type sha path`. Aplicar os mesmos filtros
   de `source/local` (extensões, `node_modules`, `dist`, diretórios ocultos, `.d.ts`).
2. Ler o conteúdo de todos os blobs com um único processo `git cat-file --batch`, mandando os SHAs
   pelo stdin. Um processo por arquivo fica lento em repositórios grandes.
3. `File.Hash` recebe o SHA do blob.
4. `ReadFile(path)`: `git show <rev>:<path>`, usado para `tsconfig.json` e `package.json` daquela versão.
5. Suportar `Subdir` para monorepos (a aplicação é uma pasta dentro do repositório).
6. Na CLI: `-rev <sha|branch>`. Sem `-rev`, usa `source/local` como hoje.

**Aceite.**
- Para o commit atual, o grafo extraído por `source/git` é igual ao extraído por `source/local`
  (mesmos nodes e edges; só `File.Hash` muda de formato).
- Um teste cria um repositório temporário com dois commits e verifica que cada `-rev` vê a sua versão
  do arquivo.

---

## Etapa 2: cache do `ir.FileGraph` por blob

**Objetivo.** Analisar N commits sem reprocessar N vezes os arquivos que não mudaram.

**Arquivos alvo.**
- `extractor/parser/cache.go` (novo, um decorator de `Parser`)

**Passos.**
1. Chave: `(linguagem, blobSHA, appKey, path, versão do extrator)`. O path entra na chave porque o
   `fileID` faz parte dos IDs gerados pelo parser. A versão do extrator entra para invalidar o cache
   quando o parser muda.
2. Armazenamento: em memória para uma execução; em disco (`.rhaxis-cache/` com gob ou JSON
   comprimido) para execuções repetidas.
3. O linker continua rodando sobre a aplicação inteira a cada commit. Ele é barato perto do parsing;
   linkagem incremental fica para depois (é o item "extração incremental" do M3).

**Aceite.**
- Analisar 20 commits seguidos faz parsing de no máximo (arquivos do primeiro commit + arquivos
  alterados nos 19 seguintes).

---

## Etapa 3: hashes de conteúdo nas declarações

**Objetivo.** Saber se o corpo de uma função mudou sem depender do diff de texto, e ignorar mudanças
que são só de posição (linhas inseridas acima).

**Arquivos alvo.**
- `entity/node.go`: campos novos
- `extractor/parser/javascript/javascript.go`: cálculo

**Passos.**
1. Adicionar em `Function`, `Class`, `Field`, `Variable` e `EnumMember`:
   - `BodyHash`: hash do texto do node no tree-sitter, normalizado (sem comentários, espaços
     colapsados). Para normalizar, percorrer os tokens folha da árvore e concatenar, o que é mais
     robusto que regex.
   - `SignatureHash` (Function): nome, parâmetros (nome, tipo, opcional, default), tipo de retorno,
     modificadores e decorators.
2. Os dois entram no JSON e no Neo4j via `entity.Properties()` sem mudança no repository.

**Aceite.**
- Inserir linhas em branco ou comentários acima de uma função não muda o `BodyHash` dela.
- Trocar `a + b` por `a - b` muda o `BodyHash`, mas não o `SignatureHash`.

---

## Etapa 4: changeset do git, de linhas para nodes

**Objetivo.** Responder "quais regiões o commit tocou" no nível de declaração.

**Arquivos alvo.**
- `extractor/change/gitdiff.go` (novo pacote `change`)

**Passos.**
1. `git diff -U0 -M --no-color <base> <head> -- <subdir>` e parse dos hunks
   (`@@ -a,b +c,d @@`) em faixas de linhas por arquivo.
2. Status por arquivo: `A` (adicionado), `D` (removido), `M` (modificado), `R` (renomeado, com o
   par de paths antigo e novo).
3. Para cada faixa:
   - linhas `+` → nodes do **grafo head** cujo `Location` contém a faixa;
   - linhas `-` → nodes do **grafo base**.
   - Escolher o node **mais interno** (menor intervalo). Para `Call`, subir para o `OwnerID` (a função dona).
4. Linhas fora de qualquer declaração (imports, código de topo) ficam atribuídas ao `File`.

**Aceite.**
- Alterar uma linha dentro de `UsersService.findOne` na fixture-a produz exatamente
  `{UsersService.findOne: modified}`.

---

## Etapa 5: diff semântico entre os grafos

**Objetivo.** Classificar cada mudança e mostrar o que mudou nas **dependências**, que é a parte
que mais ajuda a explicar um bug.

**Arquivos alvo.**
- `extractor/change/diff.go`

### 5.1 Renomeações de arquivo

O path faz parte do ID. Sem tratamento, um arquivo renomeado aparece como "tudo removido + tudo
adicionado". Usar os pares `R old → new` do `git diff -M` para reescrever no grafo base o prefixo
`<app>:<old>` para `<app>:<new>` antes de comparar.

Renomeação de símbolo dentro do arquivo (`findOne` → `findById`): heurística opcional, casando um
removido com um adicionado do mesmo tipo e mesmo `BodyHash`.

### 5.2 Nodes de declaração

Comparar por ID:

| Classe | Regra |
|---|---|
| `added` | ID só no head |
| `removed` | ID só na base |
| `signature` | `SignatureHash` diferente (ou `TypeName`, `Decorators`, modificadores em Field/Class) |
| `body` | `BodyHash` diferente, assinatura igual |
| `moved` | Só `Location` diferente. É ruído: não aparece no relatório por padrão |
| `renamed` | Via 5.1 |

### 5.3 Edges (dependências)

**Problema:** nodes anônimos têm ID por posição (`#UsersService.findOne/call@14:24`). Uma linha
inserida acima muda o ID de todos os `Call` abaixo dela, então comparar `Call` por ID gera diferenças falsas.

**Solução:** subir as edges de uso para a **declaração dona** e comparar como multiconjunto,
ignorando `Location`:

```
(dono, tipo da edge, alvo, member, index)    ex.: (UsersRepository.findById, READS, User.deletedAt, "", -)
```

- `CALLS` de um `Call` vira `(função dona, CALLS, alvo, calleeText)`.
- `ARGUMENT{i}` vira `(função dona, ARGUMENT, alvo, chamada=calleeText, i)`.
- `READS`, `WRITES`, `INSTANTIATES` e `HAS_TYPE` direto pelo dono.
- `IMPORTS` por arquivo.

O resultado por declaração é `+ CALLS cache.get`, `- READS User.deletedAt` e assim por diante.

A mesma técnica resolve um problema futuro: quando `If`, `Return` e `Assignment` virarem nodes
(M2), eles também terão ID por posição.

### 5.4 Saída

```go
type Change struct {
    NodeID   string
    NodeType entity.NodeType
    Kind     ChangeKind      // added, removed, signature, body, renamed, deps
    OldID    string          // em renomeações
    Hunks    []LineRange     // linhas tocadas (etapa 4)
    EdgesAdded   []EdgeKey
    EdgesRemoved []EdgeKey
}
type ChangeSet struct {
    Base, Head string
    Commits    []CommitInfo  // sha, autor, data, mensagem, PR (etapa 8)
    Changes    []Change
}
```

**Aceite.**
- Inserir 10 linhas no topo de um arquivo sem mudar lógica produz um changeset vazio
  (sem considerar `moved`).
- Remover a condição `deletedAt` de `UsersRepository.findById` gera
  `body` + `- READS User.deletedAt`.

---

## Etapa 6: análise de impacto e de suspeitos

**Arquivos alvo.**
- `extractor/change/impact.go`

### 6.1 Impacto (quem depende do que mudou)

A partir dos nodes alterados, percorrer **ao contrário** as edges `INVOKES`, `CALLS`, `READS`,
`HAS_TYPE`, `EXTENDS`, `IMPLEMENTS` e `IMPORTS` no grafo head.

- Profundidade máxima configurável (padrão 5).
- **Nodes de parada** (logger, utils, `External`, `Package`): entram no resultado mas a busca não
  continua a partir deles. Lista configurável e, mais tarde, detecção automática por grau de entrada
  (é o item do M3).
- Peso por `Resolution`: `exact` > `inferred` > `name_only`. Caminhos com `name_only` ficam marcados
  como incertos.
- Depois do M2: listar os `Endpoint` alcançados (`HANDLED_BY`). Isso responde "quais rotas da API
  esta mudança pode quebrar".

### 6.2 Suspeitos (qual mudança pode ter causado o bug)

Entrada: o node do sintoma (ID, ou `arquivo:linha`, ou `GET /users/{id}` depois do M2) e um
intervalo de commits.

1. Percorrer **para frente** a partir do sintoma (o que ele chama, lê e instancia) no grafo head,
   com os mesmos limites de 6.1. É o cone de dependências do sintoma.
2. Para cada commit do intervalo (etapa 7), cruzar o changeset com esse cone.
3. Ordenar os suspeitos por:
   - distância até o sintoma (menor primeiro);
   - tipo de mudança (`deps` e `signature` acima de `body`, que fica acima de `added`);
   - confiança do caminho (`Resolution`);
   - proximidade do commit em relação ao momento em que o bug apareceu.

**Exemplo de saída:**

```
Sintoma: users.controller.ts#UsersController.findOne
Cone: 23 declarações · intervalo: main~10..main (10 commits)

  #1  a1b2c3  2026-09-29  "refatora repositório"            (PR #42, autor X)
      users.repository.ts#UsersRepository.findById   body + deps     distância 2
        - READS  User.deletedAt
      caminho: UsersController.findOne → UsersService.findOne → UsersRepository.findById

  #2  d4e5f6  2026-09-27  "ajusta cache"
      lib/cache.ts#LRUCache.get                      body            distância 3

  ignorado (parada): shared/logger.ts#AppLogger.log   signature      d4e5f6
```

**Aceite.**
- Fixture com um bug introduzido de propósito (etapa 9): o commit que introduziu o bug é o primeiro
  da lista.

---

## Etapa 7: varrer um intervalo de commits

**Arquivos alvo.**
- `extractor/change/history.go`

**Passos.**
1. `git log --first-parent --format=%H%x00%P%x00%an%x00%aI%x00%s <base>..<head>`.
   `--first-parent` faz cada commit da `main` corresponder a um merge de PR.
2. Para cada commit, comparar com o primeiro pai. Com o cache por blob (etapa 2), cada passo só faz
   parsing dos arquivos daquele commit.
3. Para "o que mudou na main desde a última análise": guardar o último SHA analisado
   (`.rhaxis-cache/last` ou um node `Commit` no Neo4j) e usar `git fetch origin main` e
   `<último>..origin/main`.

**Comparação entre dois pontos vs. commit a commit:**

| Modo | Uso |
|---|---|
| `base..head` direto (um diff) | "o que mudou entre a versão em produção e a main" |
| Commit a commit | Atribuir cada mudança ao commit e PR que a introduziu (necessário para os suspeitos) |

---

## Etapa 8: metadados do GitHub (opcional)

O git local já tem tudo que a análise de código precisa. O GitHub entra só para dar contexto:

- `gh api repos/{owner}/{repo}/commits/{sha}/pulls` → número, título e descrição do PR.
- Issues vinculadas ao PR, que ajudam a ligar a mudança ao motivo dela.
- O node `Repository` (`URL`, `Provider`) já existe e indica de onde buscar.

A falta de rede ou de token não pode quebrar a análise. Sem GitHub, o relatório sai só com os dados do git.

---

## Etapa 9: fixtures e testes

**Arquivos alvo.**
- `testdata/history/build.sh`: monta um repositório git temporário a partir de passos versionados
- `extractor/change/change_test.go`

**Passos.**
1. Gerar o histórico a partir de `testdata/fixture-a-nest`, com commits conhecidos:
   1. estado inicial;
   2. só reformatação (inserir linhas, comentários) → changeset vazio;
   3. renomear `users.repository.ts` → `user.repository.ts` → só `renamed`;
   4. **bug**: remover o filtro `deletedAt` de `UsersRepository.findById`;
   5. mudança não relacionada em `shared/logger.ts`.
2. O repositório git é criado durante o teste (`t.TempDir()`), e não versionado dentro deste repo,
   porque um `.git` dentro do outro causa problemas.

**Aceite.**
- Changeset esperado para cada commit (golden files em JSON).
- Suspeitos a partir de `UsersController.findOne`: o commit 4 vem em primeiro, o 5 vem marcado como parada.

---

## Etapa 10: CLI

**Arquivos alvo.**
- `extractor/cmd/main.go` → subcomandos, ou um binário novo `extractor/cmd/diff`

```
# o que mudou entre dois pontos
rhaxis diff     -repo . -app-dir . -base origin/main~10 -head origin/main  [-format text|json|md]

# impacto de um intervalo
rhaxis impact   -repo . -base v1.4.0 -head main -depth 5

# o que pode ter causado o bug
rhaxis suspects -repo . -symptom 'src/users/users.controller.ts#UsersController.findOne' -last 20

# histórico de uma declaração (quais commits a alteraram e como)
rhaxis history  -repo . -node 'src/users/users.service.ts#UsersService.findOne' -last 50
```

`-format md` gera um texto pronto para colar num PR ou issue.

---

## Etapa 11: autoria e versionamento no grafo principal

**Objetivo.** O grafo passa a guardar o histórico completo do código (todas as versões de cada
declaração e de suas dependências) e quem escreveu cada parte. Cada reextração soma ao histórico em
vez de substituir o grafo.

Até a etapa 10 tudo roda na memória e nada muda no banco. Esta etapa muda a persistência.

**Arquivos alvo.**
- `entity/node.go`: `Person`, `Commit` e os nodes de versão (`FunctionVersion` etc.)
- `entity/edge.go`: `AUTHORED_BY`, `COMMITTED_BY`, `CHANGED`, `HAS_VERSION`, `PRODUCED`,
  `RENAMED_TO`, `CREATED`, `LAST_MODIFIED`, `OWNS`
- `extractor/repository/repository.go`: interface `HistoryRepository`
- `extractor/repository/neo4j/neo4j.go`: implementação de `ApplyCommit`
- `extractor/change/blame.go` (novo)
- `extractor/extractor.go`: modo histórico (`RunHistory`)

### 11.1 Por que o `ReplaceApplication` atual não serve

Hoje cada extração faz `DETACH DELETE` em todos os nodes da aplicação e grava tudo de novo
(`deleteApp` em `extractor/repository/neo4j/neo4j.go`). Isso apaga o passado. Para guardar histórico,
a persistência precisa aplicar **só as diferenças de cada commit** (o changeset da etapa 5).

### 11.2 Eixo do tempo

- **O tempo é o commit, não a hora da extração.** Cada commit da `main` recebe um `seq` (posição no
  `git log --first-parent --reverse`). Como o histórico do `first-parent` é linear, um inteiro basta.
- **Reextrair o mesmo commit não muda nada.** `ApplyCommit` recusa (ou ignora) `seq <= LastCommit`.
- **Código não commitado** (diretório de trabalho) gera um estado temporário, que pode ser consultado
  mas não entra no histórico. Ele pode ficar em outra aplicação (`app:<key>@working`) ou só em memória.
- **Branches** ficam fora no começo. Suportá-las exige trocar o `seq` linear pelo DAG de commits
  (`PARENT`), o que fica para depois.

### 11.3 Opções de armazenamento

| | A. Snapshot por commit | B. Validade em node e edge | **C. Identidade + versões** |
|---|---|---|---|
| Ideia | Grafo inteiro por commit (`appKey = api@<sha>`) | `validFrom`/`validTo` em tudo | Node fixo com o estado atual + nodes de versão |
| Espaço | Tamanho do grafo × commits | Só o que muda | Só o que muda |
| Guarda propriedades antigas (assinatura, tipo, decorators) | Sim | Não: um node não guarda dois valores da mesma propriedade | Sim, nas versões |
| Consulta do estado atual | Filtra pelo commit | `WHERE validTo IS NULL` em toda consulta | **Igual a hoje** |
| IDs entre versões | Mudam (prefixo com SHA) | Estáveis | Estáveis |
| Esforço | Baixo | Médio | Médio/alto |

**Recomendação: C.** A opção A continua útil para depuração pontual (comparar dois commits no Neo4j
Browser) e não exige código novo.

### 11.4 Modelo de versionamento (opção C)

```
(:Function {id, ...estado atual, validFrom, validTo})          identidade: o ID estável que já existe
   -[:HAS_VERSION]->
(:FunctionVersion {id: "<funcId>@<seq>", bodyHash, signatureHash, typeName, decorators,
                   startLine, endLine, validFrom, validTo})
   <-[:PRODUCED]-(:Commit)

(Function)-[:CALLS|READS|WRITES|INSTANTIATES|HAS_TYPE {validFrom, validTo, ...}]->(declaração)
(File)-[:IMPORTS {validFrom, validTo}]->(File|Package)
(old)-[:RENAMED_TO {commit}]->(new)
```

**Regras.**

1. **Identidade.** O node de declaração (`Function`, `Class`, `Field`, `Interface`, `Enum`,
   `EnumMember`, `Variable` de topo) mantém o ID atual e as propriedades do estado mais recente.
   Consultas sobre o código atual continuam iguais às de hoje, só com `WHERE n.validTo IS NULL` quando
   for preciso excluir declarações removidas.
2. **Quando criar uma versão.** Só quando `BodyHash` ou `SignatureHash` mudam (etapa 3). Mudança só
   de linha atualiza o node atual sem criar versão; sem essa regra, o histórico vira ruído.
3. **Edges de dependência.** A validade fica na edge entre declarações, comparadas pela função dona
   como na etapa 5.3. Uma dependência removida recebe `validTo` e não é apagada. A edge que volta a
   existir depois gera uma nova edge com outro `validFrom`.
4. **`Call` e outros nodes anônimos não têm histórico.** Só o estado atual deles fica guardado.
   Quando o corpo da função dona muda, os `Call` dela são apagados e recriados. O histórico de
   dependências fica nas edges levadas para a função dona. Isso segura o tamanho do banco, já que os
   `Call` são a maioria dos nodes.
5. **Declarações removidas** recebem `validTo` no node de identidade e na última versão, mas o node
   fica, junto com os autores e o histórico.
6. **Renomeação de arquivo.** Como o path faz parte do ID, a renomeação (`git diff -M`) cria
   identidades novas ligadas às antigas por `RENAMED_TO {commit}`. As consultas de histórico seguem
   essa edge (`-[:RENAMED_TO*0..]->`). Renomeação de símbolo detectada pela heurística da etapa 5.1
   usa a mesma edge.

**Índices.** `id` (único) em todos os labels, `validTo` nos labels de declaração, `seq` e `sha` em
`Commit`, `email` em `Person`.

### 11.5 Autoria

**Fontes.**

| Fonte | O que responde | Limitação |
|---|---|---|
| `git blame --porcelain -w -M -C <rev> -- <arquivo>` | Quem escreveu cada linha que existe hoje | Não sabe quem criou a função, se as linhas originais já foram reescritas |
| Changesets do histórico (etapas 5 e 7) | Quem criou, quem alterou o corpo, quem mudou a assinatura | Precisa processar o histórico desde o início |

As duas se complementam. O blame é levado até as declarações com a técnica da etapa 4: cada linha vai
para a declaração mais interna que a contém, e as linhas são somadas por autor.

**Modelo.**

```
(:Person {id: "person:<email normalizado>", name, email, githubLogin})
(:Commit {id: "commit:<sha>", sha, seq, date, message, pr})

(Commit)-[:AUTHORED_BY]->(Person)
(Commit)-[:COMMITTED_BY]->(Person)                         quando é diferente do autor (rebase, squash)
(Commit)-[:CHANGED {kind: added|body|signature|deps|removed|renamed}]->(declaração)
(Commit)-[:PRODUCED]->(FunctionVersion|ClassVersion|...)

derivadas, recalculadas para as declarações tocadas em cada commit:
(Person)-[:CREATED {commit}]->(declaração)                 autor do commit em que o ID apareceu
(Person)-[:LAST_MODIFIED {commit}]->(declaração)           autor do último CHANGED de corpo/assinatura
(Person)-[:OWNS {lines, share}]->(declaração)              blame atual: X tem 70% das linhas
```

**Decisões.**

- **`Person` é um node, não uma propriedade**, para perguntar "o que X tocou" e "quem conhece este módulo".
- **Unificação de identidade.** O mesmo dev com dois emails vira um `Person` só: usar o `.mailmap`
  do repositório e, quando houver GitHub, o login do autor no commit (etapa 8).
- **Autoria só em declarações**, nunca em `Call`, pelo mesmo motivo da regra 4 de 11.4.
- **Commits de formatação.** `-w` no blame e um `.git-blame-ignore-revs` para commits de prettier
  e lint. Também ignorar esses commits nas edges `LAST_MODIFIED`; sem isso, quem rodou o formatador
  vira dono de tudo. Commits cujo changeset é vazio (etapa 5) já ficam de fora naturalmente.
- **Privacidade (LGPD).** Email é dado pessoal. Configuração para guardar o email, só um hash dele,
  ou só o login.
- **Custo.** O blame roda só nos arquivos tocados em cada commit, nunca no repositório inteiro.

### 11.6 Interface e fluxo

```go
type HistoryRepository interface {
    // Aplica o changeset de UM commit: fecha validTo do que mudou ou saiu, cria versões e edges novas,
    // grava Commit/Person/CHANGED e recalcula OWNS/CREATED/LAST_MODIFIED das declarações tocadas.
    // Tudo numa transação: um commit é aplicado inteiro ou não é aplicado.
    ApplyCommit(ctx context.Context, app entity.Application, c CommitInfo,
        cs *change.ChangeSet, head *entity.Graph, blame []change.Ownership) error
    // Último commit aplicado, para retomar a extração incremental
    LastCommit(ctx context.Context, app entity.Application) (seq int, sha string, err error)
}
```

`GraphRepository.ReplaceApplication` continua existindo para o modo sem histórico (JSON, testes,
extração de código não commitado).

**Fluxo.**

1. **Carga inicial (backfill).** Percorrer todos os commits da `main`, do primeiro ao HEAD, e chamar
   `ApplyCommit` em cada um. O cache por blob (etapa 2) torna isso viável. Para repositórios muito
   longos, começar de um commit recente (`-since <sha>`): o estado dele é gravado como uma versão
   inicial, sem autoria por criação, e o blame ainda preenche `OWNS`.
2. **Incremental.** `LastCommit` → `git fetch origin main` → aplicar `último..origin/main`, um
   commit por vez, em ordem.
3. **Recuperação.** Como cada `ApplyCommit` é uma transação e o `seq` só avança, uma falha no meio
   da carga é retomada do último commit aplicado.
4. **Mudança no extrator.** Se o parser mudar (ex.: passa a extrair `If`), o histórico antigo fica
   incoerente com o novo. Guardar a versão do extrator em `Commit` e oferecer um `-rebuild` que
   refaz a carga inicial.

### 11.7 Consultas que passam a ser possíveis

```cypher
// quem conhece esta função
MATCH (p:Person)-[o:OWNS]->(f:Function {id: $id})
RETURN p.name, o.share ORDER BY o.share DESC

// histórico completo de uma função, seguindo renomeações
MATCH (f:Function {id: $id})<-[:RENAMED_TO*0..]-(old)<-[ch:CHANGED]-(c:Commit)-[:AUTHORED_BY]->(p)
RETURN c.date, c.sha, ch.kind, p.name, c.pr ORDER BY c.seq

// como era a função no commit X
MATCH (f:Function {id: $id})-[:HAS_VERSION]->(v)
WHERE v.validFrom <= $seq AND (v.validTo IS NULL OR v.validTo > $seq)
RETURN v

// as dependências como estavam no commit X
MATCH (a)-[r:CALLS|READS]->(b)
WHERE r.validFrom <= $seq AND (r.validTo IS NULL OR r.validTo > $seq)
RETURN a, r, b

// o que X mudou no último mês que alcança o endpoint (depois do M2)
MATCH (p:Person {email: $e})<-[:AUTHORED_BY]-(c:Commit)-[:CHANGED]->(f)
WHERE c.date > datetime() - duration('P30D')
MATCH path = (:Endpoint {path: '/users/{id}'})-[:HANDLED_BY]->()-[:INVOKES*0..5]->(f)
RETURN c, f, path

// pontos quentes: declarações que mais mudaram e quantas pessoas mexeram nelas
MATCH (c:Commit)-[:CHANGED]->(f:Function)
MATCH (c)-[:AUTHORED_BY]->(p)
RETURN f.id, count(DISTINCT c) AS mudancas, count(DISTINCT p) AS autores
ORDER BY mudancas DESC LIMIT 20
```

A busca de suspeitos da etapa 6.2 também passa a rodar direto no banco, sem recalcular os diffs.

### 11.8 Sub-etapas

| # | Entrega | Risco |
|---|---|---|
| 11a | `Commit`, `Person`, `AUTHORED_BY` e `CHANGED`, mantendo o `ReplaceApplication` para o estado atual. Os `CHANGED` apontam para IDs; os removidos ficam sem node | Baixo: já mostra quem mudou o quê e quando |
| 11b | `ApplyCommit` no lugar do `ReplaceApplication`: validade nos nodes de declaração e nas edges de dependência, `RENAMED_TO` | Médio: muda a persistência |
| 11c | Nodes de versão (`HAS_VERSION`, `PRODUCED`) | Baixo, depois da 11b |
| 11d | Blame: `OWNS`, `CREATED`, `LAST_MODIFIED`, `.mailmap`, ignore-revs | Baixo |
| 11e | Carga inicial, retomada e `-rebuild` | Médio: desempenho em repositórios grandes |

**Aceite.**
- Rodar `ApplyCommit` duas vezes no mesmo commit não altera o banco.
- Na fixture de histórico (etapa 9): `UsersRepository.findById` tem 2 versões; a edge
  `READS User.deletedAt` tem `validTo` igual ao `seq` do commit 4; o arquivo renomeado no commit 3
  tem `RENAMED_TO` e o histórico da função atravessa a renomeação.
- O commit de reformatação (passo 2 da fixture) não gera `CHANGED` nem altera `LAST_MODIFIED`.
- O estado atual consultado com `validTo IS NULL` é igual ao grafo extraído por `ReplaceApplication`
  no HEAD.

---

## Etapa 12: rhaxis no CI/CD

**Objetivo.** Rodar a análise automaticamente nos eventos do ciclo de entrega (PR, merge, deploy,
incidente), para que o impacto de uma mudança seja visto **antes** do merge e a causa de um bug seja
encontrada **depois** do deploy sem investigação manual.

**Depende de.** Etapas 1–7 e 10. O modo com estado (12.2) depende da etapa 11. Tudo que fala de
endpoint e contrato depende do M2; impacto entre aplicações depende de `REQUESTS`.

### 12.1 Eventos e o que o rhaxis faz em cada um

| Evento | Ação | Resultado |
|---|---|---|
| PR aberto ou atualizado | Diff `merge-base..head`, impacto, testes afetados, revisores sugeridos, regras | Comentário no PR, check com anotações nas linhas, labels, status da regra |
| Merge na `main` | `ApplyCommit` no grafo central (etapa 11) | Histórico e autoria atualizados |
| Deploy em um ambiente | Grava `Deployment` apontando para o commit | Saber exatamente qual código está em cada ambiente |
| Release ou tag | Diff entre a tag anterior e a atual | Notas de release por endpoint e por módulo |
| Incidente ou alerta | Stack trace → sintoma → suspeitos entre o deploy anterior e o atual | Comentário na issue do incidente com os PRs candidatos a revert |
| Agendado (noturno) | Pontos quentes, consistência do grafo, `-rebuild` quando o extrator muda | Relatório de saúde do código |

### 12.2 Dois modos de execução

**A. Sem estado (só o CI).** O job do PR extrai a base e o head, faz o diff e reporta. Não precisa de
banco.

- Prós: funciona em qualquer repositório, inclusive PRs de forks (sem segredos), sem infraestrutura.
- Contras: não sabe nada de outras aplicações nem de histórico além do que o `git log` do checkout
  alcança; sem revisores por `OWNS` a não ser rodando o blame na hora.

**B. Com estado (grafo central).** O merge na `main` alimenta um Neo4j central. O job do PR extrai só
o head, lê o estado da `main` no grafo central e calcula o delta.

- Prós: impacto entre aplicações, autoria e histórico prontos, PR mais rápido (a base não é extraída).
- Contras: infraestrutura para manter; credenciais no CI; o grafo central precisa estar em dia com a
  `main` (se o job de ingestão falhar, o PR compara com um estado velho, então o PR precisa conferir
  `LastCommit` contra o `merge-base` e cair no modo A quando estiverem diferentes).

**Recomendação:** começar pelo **A** e ligar o **B** quando a etapa 11 estiver pronta. O mesmo
binário suporta os dois; o modo B é ativado quando há `RHAXIS_GRAPH_URL` configurado.

Para o modo B, é melhor colocar um **serviço rhaxis** (API pequena na frente do Neo4j) do que dar
acesso direto ao banco ao CI: o serviço controla quem escreve (só o job da `main`) e quem lê, e
esconde o Cypher dos workflows.

### 12.3 PR: relatório de impacto

**Exemplo de comentário** (um único comentário por PR, atualizado a cada push e identificado por um
marcador `<!-- rhaxis -->`):

```markdown
### rhaxis · impacto do PR

**7 declarações alteradas** em 3 arquivos · **2 endpoints alcançados** · risco: **médio**

| Declaração | Mudança | Dependências |
|---|---|---|
| `UsersRepository.findById` | corpo | − READS `User.deletedAt` |
| `UsersService.findOne` | assinatura | parâmetro `includeDeleted?: boolean` |

**Endpoints alcançados**
- `GET /users/{id}` ← UsersController.findOne → UsersService.findOne → UsersRepository.findById
- `DELETE /users/{id}` ← ... (caminho com resolução `inferred`)

**Contrato:** sem mudança.
**Testes afetados:** 4 de 112 (`users.service.spec.ts`, `users.controller.spec.ts`)
**Sugestão de revisão:** @fulana (62% de `users.repository.ts`), @ciclano (última alteração em `findOne`)

<details><summary>Ignorados</summary>
`shared/logger.ts#AppLogger.log` (node de parada)
</details>
```

**Outras saídas.**
- **Check run** com anotações nas linhas alteradas: "esta função é usada por 2 endpoints".
- **Labels** derivados dos módulos tocados (`area:users`) e do risco (`risk:medium`).
- **JSON** do changeset como artefato do job, para outras ferramentas.
- Saída em **SARIF** é possível (aparece na aba de code scanning), mas é pensada para alertas de
  segurança; usar só se fizer sentido para as regras da 12.5.

**Revisores sugeridos.** Ordenar por `OWNS.share` e `LAST_MODIFIED` nas declarações tocadas,
excluindo o autor do PR e pessoas inativas há mais de N meses, no máximo 2 nomes. Começar só
sugerindo no comentário; pedir revisão automaticamente só depois que as sugestões forem confiáveis.
É um complemento ao `CODEOWNERS`, que é por path e não por função.

### 12.4 Seleção de testes afetados

**Ideia.** Arquivos de teste também são extraídos. Uma função de teste que alcança (por `CALLS`,
`INVOKES`, `INSTANTIATES`) uma declaração alterada é um teste afetado. O PR roda só esses testes
primeiro, e a suíte completa depois ou só na `main`.

**Passos.**
1. Marcar arquivos de teste (`*.spec.ts`, `*.test.js`, `__tests__/`) com `File.Test = true`.
2. Tratar `describe`/`it`/`test` como nodes de teste (ou pelo menos o arquivo de teste como unidade).
3. Percorrer ao contrário a partir das declarações alteradas até os arquivos de teste.
4. Saída: lista de arquivos no formato do test runner (`jest --runTestsByPath ...`).

**Cuidados.**
- **Na dúvida, roda tudo.** Mudança em configuração (`package.json`, `tsconfig.json`, `jest.config`),
  em arquivo que o extrator não leu, ou com caminho `name_only` → suíte completa.
- **Mocks e injeção de dependência** (Nest `Test.createTestingModule`) escondem chamadas do grafo
  estático. Até a injeção por token do M2, testes de módulo Nest tendem a não ser selecionados.
- **Medir antes de confiar:** rodar por um período a seleção junto com a suíte completa e registrar
  quantas falhas da suíte completa a seleção teria pego (recall). Só usar a seleção para encurtar o
  pipeline com recall perto de 100%.

### 12.5 Regras (quality gates)

Configuradas por repositório em `.rhaxis.yaml`:

```yaml
version: 1
app:
  key: users-api
  dir: .                      # monorepo: uma entrada por aplicação
mode: advisory                # advisory (só comenta) | enforce (pode falhar o check)

stop_nodes:                   # a travessia de impacto não passa por aqui
  - "src/shared/logger.ts#*"
  - "src/shared/utils/**"

critical:                     # áreas que exigem atenção extra
  - name: auth
    match: ["src/auth/**"]
    require_reviewers: 1      # de quem tem OWNS nessas declarações
  - name: billing
    match: ["src/billing/**", "endpoint:POST /payments"]

rules:
  contract_breaking:          # depois do M2
    level: error              # endpoint removido, HttpParam obrigatório novo, tipo de resposta mudou
    allow_label: breaking-change-approved
  exported_signature_changed:
    level: warning            # assinatura de export mudou e há consumidores
  blast_radius:
    level: warning
    max_endpoints: 5
  unresolved_increase:
    level: warning            # o PR aumentou o número de External/CALLS ausentes
    max_delta: 10

tests:
  select: advisory            # off | advisory | enforce
  patterns: ["**/*.spec.ts", "**/*.test.js"]

authorship:
  store_email: hash           # plain | hash | none
  ignore_revs: .git-blame-ignore-revs
```

**Princípio:** tudo começa em `advisory`. Uma regra só passa para `enforce` depois de algumas semanas
com poucos falsos positivos. Um check que falha sem motivo real é desligado pelo time e leva o resto
junto.

**Exemplos de regras com o grafo que outras ferramentas não conseguem fazer:**
- Quebra de contrato HTTP: `GET /users/{id}` passou a exigir um query param.
- Quebra entre aplicações: a aplicação B chama (`REQUESTS`) um endpoint que este PR removeu.
- Mudança em função de área crítica sem revisão de quem conhece aquele código.
- Uma função de pagamento passou a chamar uma função que antes ela não alcançava (nova dependência
  num caminho crítico).

### 12.6 CD: deploys, releases e incidentes

**Marcar deploys.**

```
(:Deployment {id, env, version, at, status})-[:DEPLOYS]->(:Commit)
(:Deployment)-[:PREVIOUS]->(:Deployment)          por ambiente
```

O pipeline de deploy chama `rhaxis ci deploy -env prod -sha $SHA` ao terminar. Com isso:
- "O que está em produção" é uma consulta, não uma suposição.
- **O intervalo de suspeitos de um bug em produção é exato:** `deploy anterior..deploy atual`.

**Risco do deploy.** Um score que soma: mudanças de contrato, declarações alteradas em áreas
críticas, endpoints alcançados, pontos quentes tocados (declarações que mudam muito) e quanto do
código alterado o autor conhece (`OWNS` do autor nas declarações tocadas). Serve para escolher
canário ou deploy completo, ou para pedir aprovação manual. É uma heurística: mostrar os fatores que
levaram ao score, não só o número.

**Notas de release.** Diff entre tags agrupado por endpoint e módulo, com os PRs de cada mudança.
Muito mais útil para quem consome a API do que a lista de mensagens de commit.

**Incidentes.**
1. Gatilho: webhook do sistema de erros (Sentry etc.), alerta, ou `workflow_dispatch` manual com
   a stack trace.
2. Cada frame `arquivo:linha` vira uma declaração (etapa 4, usando o commit do deploy atual).
3. `rhaxis suspects` com o sintoma = frame mais interno do código da aplicação e intervalo =
   `deploy anterior..deploy atual`.
4. Resultado comentado na issue do incidente: PRs candidatos, caminho até o sintoma e quem conhece
   o código (`OWNS`), para saber quem chamar.

### 12.7 Exemplo: GitHub Actions

**PR (modo A).**

```yaml
name: rhaxis-pr
on:
  pull_request:
permissions:
  contents: read
  pull-requests: write
  checks: write
jobs:
  impact:
    runs-on: ubuntu-latest
    container: ghcr.io/bmokarzel/rhaxis:v1       # binário já compilado com cgo e tree-sitter
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0                          # precisa do merge-base e do histórico para o blame
      - uses: actions/cache@v4
        with:
          path: .rhaxis-cache
          key: rhaxis-${{ hashFiles('.rhaxis.yaml') }}-v1-${{ github.sha }}
          restore-keys: rhaxis-${{ hashFiles('.rhaxis.yaml') }}-v1-
      - run: |
          rhaxis ci pr \
            -base "${{ github.event.pull_request.base.sha }}" \
            -head "${{ github.event.pull_request.head.sha }}" \
            -report github
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

**Merge na `main` (modo B).**

```yaml
name: rhaxis-ingest
on:
  push:
    branches: [main]
concurrency:
  group: rhaxis-ingest-${{ github.repository }}
  cancel-in-progress: false                     # os commits precisam ser aplicados em ordem
jobs:
  ingest:
    runs-on: ubuntu-latest
    container: ghcr.io/bmokarzel/rhaxis:v1
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - run: rhaxis ci ingest -branch main      # aplica LastCommit..HEAD, um commit por vez
        env:
          RHAXIS_GRAPH_URL: ${{ secrets.RHAXIS_GRAPH_URL }}
          RHAXIS_GRAPH_TOKEN: ${{ secrets.RHAXIS_GRAPH_WRITE_TOKEN }}
```

Pontos importantes:
- **`concurrency` com `cancel-in-progress: false`** na ingestão: dois merges seguidos não podem ser
  aplicados fora de ordem nem cancelados no meio. Como `ingest` aplica `LastCommit..HEAD`, um job que
  falhou é recuperado pelo próximo.
- **Imagem com o binário pronto.** Compilar tree-sitter com cgo a cada execução custa minutos.
- **Cache por blob** (etapa 2) no `actions/cache`. A chave inclui a versão do extrator para invalidar
  quando o parser mudar.

### 12.8 Segurança

- **PRs de forks** rodam sem segredos e com token só de leitura. Neles, só o modo A.
  Para comentar nesses PRs, usar o padrão de dois workflows: o job do PR gera o relatório como
  artefato, e um workflow `workflow_run` (com permissão de escrita, sem fazer checkout do código do
  PR) publica o comentário. **Nunca** usar `pull_request_target` fazendo checkout do código do PR.
- **Escrita no grafo central só no job da `main`.** O job de PR usa um token só de leitura.
- **O código do PR não é executado.** O extrator só faz parsing com tree-sitter, o que reduz o risco,
  mas ainda vale limitar tamanho de arquivo, número de arquivos e tempo, porque o PR pode trazer
  arquivos gigantes de propósito.
- **Resumo por LLM** (12.9) manda código para um serviço externo: precisa de opt-in por repositório.

### 12.9 Resumo em linguagem natural (opcional)

Passar o changeset (estruturado e pequeno) mais os caminhos até os endpoints para um LLM e gerar um
parágrafo no topo do comentário, por exemplo: "o PR remove o filtro de soft-delete usado por
`GET /users/{id}`; usuários apagados voltam a ser retornados". Tende a ser bem mais confiável que
mandar o diff textual bruto, porque o modelo recebe as dependências já resolvidas. Ligado por
configuração, sem segredos em PRs de forks.

### 12.10 Arquitetura no código

```
extractor/cmd                    subcomandos: ci pr | ci ingest | ci deploy | ci incident | tests select
extractor/change                 diff, impacto, suspeitos (etapas 4–7)
extractor/ci/config              leitura do .rhaxis.yaml
extractor/ci/rules               regras da 12.5: recebem ChangeSet + grafo, devolvem []Finding
extractor/ci/report              interface Reporter
extractor/ci/report/github       comentário (upsert pelo marcador), check run, labels
extractor/ci/report/gitlab       nota no MR, code quality report
extractor/ci/report/stdout       texto, markdown e JSON (uso local e outros CIs)
```

A análise não sabe em qual plataforma está rodando; só o `Reporter` sabe. Rodar
`rhaxis ci pr -report stdout` localmente mostra o mesmo relatório que o PR vai receber.

### 12.11 Fases

| Fase | Entrega | Depende de |
|---|---|---|
| F1 | Comentário de impacto no PR, modo A, `advisory` | Etapas 1–7, 10 |
| F2 | Ingestão na `main` e marcação de deploys (modo B) | Etapa 11 |
| F3 | Revisores sugeridos e seleção de testes em `advisory`, medindo o recall | F2 (ou blame na hora) |
| F4 | Regras em `enforce` (contrato, área crítica) | M2, histórico de falsos positivos da F1 |
| F5 | Fluxo de incidente e risco de deploy | F2 |
| F6 | Regras entre aplicações (`REQUESTS`) | Item "Depois" da arquitetura |

**Como saber se está funcionando.**
- **Ruído:** fração de comentários com reação negativa ou regras ignoradas por label. Se subir, a
  regra volta para `advisory`.
- **Acerto do impacto:** nos bugs que aparecem depois do merge, a declaração com problema estava no
  relatório de impacto do PR?
- **Seleção de testes:** recall (12.4) e minutos de CI economizados.
- **Incidentes:** o PR revertido estava entre os 3 primeiros suspeitos?
- **Tempo:** o job de PR precisa caber em ~2 minutos para ninguém querer desligá-lo.

### 12.12 Correlação com produção

Além do fluxo de incidente (12.6), dá para mapear logs e spans para nodes (`arquivo:linha` →
declaração, pela etapa 4) e marcar no grafo quais funções aparecem em erros com frequência. Isso
alimenta o risco de deploy (mexer em função que já falha muito) e os pontos quentes. Liga com o
`Telemetry`/`EMITS` previsto em `entity/node.go`.

---

## Riscos e limitações

| Risco | Mitigação |
|---|---|
| Extrator do M1 ainda não validado | Fazer a validação antes; a etapa 1 tem um teste de equivalência com `source/local` |
| IDs por posição (Call, funções anônimas) mudam com qualquer edição | Comparar por dono (5.3); nunca comparar nodes anônimos por ID |
| Renomeação de arquivo quebra IDs | `git diff -M` + reescrita de prefixo (5.1) |
| Resolução fraca (`name_only`, `External`) gera caminhos falsos ou faltando no impacto | Mostrar a confiança no relatório; avançar nas etapas de `docs/extraction-gaps-plan.md` |
| Arquivo com erro de parsing em um commit antigo | O `Report.Failed` já existe: o relatório lista os arquivos ignorados naquele commit |
| Mudança em config (`tsconfig.json`, `package.json`) altera a resolução sem alterar código | Tratar como mudança de nível `Application`; o diff de edges `IMPORTS` mostra o efeito |
| Bug causado por dado, ambiente ou dependência externa | Fora do alcance da análise de código; o relatório deixa claro que só cobre o que está no grafo |
| Commits de formatação viram "autoria" de tudo | `-w` no blame, `.git-blame-ignore-revs`, changesets vazios não geram `CHANGED` |
| Email de autor é dado pessoal (LGPD) | Configurar para guardar só hash ou login (11.5) |
| Mudança no extrator deixa o histórico antigo incoerente | Versão do extrator em `Commit` e `-rebuild` (11.6) |
| Regras do CI com falsos positivos fazem o time desligar o check | Tudo começa em `advisory`; medir ruído antes de `enforce` (12.5, 12.11) |
| Grafo central atrasado em relação à `main` | PR confere `LastCommit` × `merge-base` e cai no modo sem estado (12.2) |
| Monorepo com várias aplicações | `Subdir` em `source/git`; um changeset por aplicação |

---

## Ordem sugerida

| # | Etapa | Depende de | Entrega |
|---|---|---|---|
| 0 | Validação do M1 | n/a | Extrator confiável |
| 1 | `source/git` | 0 | Extrair qualquer commit |
| 2 | Cache por blob | 1 | Vários commits sem custo alto |
| 3 | `BodyHash` / `SignatureHash` | 0 | Detectar mudança de corpo sem diff de texto |
| 4 | Changeset do git | 1 | "Quais regiões o commit tocou" |
| 5 | Diff semântico | 3, 4 | "O que mudou e em quais dependências" |
| 9 | Fixtures de histórico | 1 | Testes das etapas 4–7 (fazer junto com a 4) |
| 6 | Impacto e suspeitos | 5 | "Qual mudança pode ter causado o bug" |
| 7 | Varredura de intervalo | 2, 5 | Atribuição por commit e PR |
| 10 | CLI | 6, 7 | Uso no dia a dia |
| 8 | Metadados do GitHub | 7 | Contexto do PR |
| 11 | Autoria e versionamento no grafo | 5, 7 | Histórico completo e quem escreveu cada declaração |
| 12 | rhaxis no CI/CD | 10 (modo A); 11 (modo B) | Impacto no PR, ingestão na `main`, deploys, incidentes |

O primeiro marco útil é **0 → 1 → 4**. Com ele já dá para ver quais funções cada commit tocou,
mesmo antes do diff semântico.
