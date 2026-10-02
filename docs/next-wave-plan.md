# Plano de implementação: git + cross-service

Entregável pronto pra execução. Depois das Ondas 1–7 do parser
(`docs/implementation-status.md`), este plano cobre apenas duas peças:

1. **Git como fonte** (`source/git`) e nodes `Commit`, `Person`, `Team`.
2. **Link cross-service por inferência**: usa só o que já está no grafo, não exige
   manifesto do usuário; confirma com humano apenas quando há ambiguidade real.

Fora de escopo (ficam para depois):
- Config central `.rhaxis.yaml` com schema cheio — por enquanto só os poucos campos
  necessários pra ativar/desativar authorship.
- Extração de libraries externas/internas (`docs/multi-service-telemetry-plan.md` parte 3
  e sprint deferida).
- SDK resolver de cross-service (precisa de libs extraídas).
- Telemetria/observabilidade (`docs/multi-service-telemetry-plan.md` partes 1 e 4).
- Versionamento completo com `FunctionVersion`/validade temporal
  (`change-analysis-plan.md` 11b–11e).
- CI/CD, PR comment, deploy marking.

Este plano usa **autoria mínima** (quem mudou o quê) sem o versionamento completo.

---

## 1. Git como fonte (`source/git`)

### 1.1 Novo pacote

```
extractor/source/git/
  git.go           // Provider que lê de uma revisão sem checkout
  git_test.go
  blame.go         // git blame porcelain → ownership por linha
  log.go           // git log → CommitInfo
```

### 1.2 Interface e implementação

Mesma `source.Provider` que `local` implementa hoje. Diferença: lê de blobs do git
em vez do filesystem.

```go
type Provider struct {
    Repo   string          // caminho do repo (.git)
    Rev    string          // SHA, branch, tag
    Subdir string          // monorepo: subpasta a extrair
    Exts   []string        // mesmas extensões do registry
}

func (p *Provider) Walk(ctx context.Context, fn func(source.File) error) error
func (p *Provider) ReadFile(path string) ([]byte, error)
```

**Implementação (opção A do `change-analysis-plan.md` 1):**
- `git -C <repo> ls-tree -r -z --full-tree <rev> -- <subdir>` → lista `(mode, type, sha, path)`.
- Filtra extensões, `node_modules`, `dist`, `.d.ts`, ocultos (mesmo filtro de `local`).
- Lê blobs em lote com **um** processo `git cat-file --batch` (stdin: SHAs; stdout: conteúdos).
- `File.Hash` passa a ser o SHA do blob.

### 1.3 CLI

```
rhaxis extract -repo . -app myapp -rev HEAD                 # igual ao local, mas via git
rhaxis extract -repo . -app myapp -rev main~5
rhaxis extract -repo . -app myapp -rev v1.4.0 -subdir services/users-api
```

Sem `-rev`, continua usando `source/local` (modo atual).

### 1.4 Aceite

- Teste cria repo temporário com 2 commits, confirma que `-rev <sha1>` e `-rev <sha2>` geram
  grafos com conteúdos distintos.
- Teste de equivalência: `-rev HEAD` produz o mesmo grafo que `source/local` no mesmo diretório
  (só `File.Hash` muda de formato).

---

## 2. `Commit`, `Person` e `Team`

### 2.1 Nodes

```go
// entity/node.go

type Commit struct {
    Base
    SHA         string
    Message     string
    AuthoredAt  time.Time
    CommittedAt time.Time
    Parent      string      // primeiro parent (first-parent linear)
}

type Person struct {
    Base
    Name        string
    Email       string      // normalizado via .mailmap; hash se config pedir (ver 2.5)
    GithubLogin string      // opcional, só se config pedir
}

type Team struct {
    Base
    Name    string
    Members []string        // IDs de Person (denormalizado para query rápida)
}
```

Novos `NodeType`: `CommitNode`, `PersonNode`, `TeamNode`.

### 2.2 Edges

```go
AuthoredByEdge   EdgeType = "AUTHORED_BY"    // Commit → Person
CommittedByEdge  EdgeType = "COMMITTED_BY"   // Commit → Person (quando ≠ autor)
ChangedEdge      EdgeType = "CHANGED"        // Commit → declaração
                                               //   Properties: kind (added|body|signature|deps|renamed|removed)
MemberOfEdge     EdgeType = "MEMBER_OF"      // Person → Team
OwnsEdge         EdgeType = "OWNS"           // Person|Team → declaração|File|Package
                                               //   Properties: source (blame|codeowners|config), share (0..1)
CreatedEdge      EdgeType = "CREATED"        // Person → declaração (primeiro commit)
LastModifiedEdge EdgeType = "LAST_MODIFIED"  // Person → declaração (último CHANGED body/signature)
```

**Decisão explícita:** autoria só sai para **declarações** (Function, Class, Field, Variable
de topo, Interface, Enum, EnumMember). Nunca para `Call`, `Assignment`, `If`, etc. — nodes
anônimos têm ID por posição e virariam ruído.

### 2.3 Fonte de dados (Person)

Vem do git direto, sem config:

```
git log --format='%H%x00%an%x00%ae%x00%cn%x00%ce%x00%aI%x00%cI%x00%s' <range>
```

Normalização de identidade:
- Email em lowercase.
- `.mailmap` do repo (git já aplica se existir).
- `Person.ID = "person:" + sha1(email_normalizado)`.
- Mesmo dev com dois emails → se `.mailmap` resolve, vira um só; senão fica como dois Person
  distintos (comando `rhaxis people merge` futuro resolve manualmente).

### 2.4 Fonte de dados (Team)

Git não tem noção de time. Duas fontes, nessa ordem de preferência:

**Fonte A — CODEOWNERS (preferida, convenção já existente):**

```
# .github/CODEOWNERS
src/users/**        @empresa/time-identidade
src/billing/**      @empresa/time-pagamentos @alice
src/shared/logger/* @alice
```

Parser lê `.github/CODEOWNERS`, extrai pares `(path_pattern, [owner])`. Owner que começa com
`@org/` é team; owner que começa só com `@` é pessoa.

**Fonte B — `.rhaxis-teams.yaml` (opcional, só se CODEOWNERS não cobrir):**

```yaml
version: 1
teams:
  - name: time-identidade
    members: [alice@empresa.com, bob@empresa.com]
    owns:
      - "src/users/**"
```

Nenhuma das duas fontes é obrigatória. Se nenhuma existir, `Team` simplesmente não é criado —
`Person`, `Commit` e `OWNS` por blame continuam funcionando.

### 2.5 Config mínima de authorship

Como o Sprint 2 (config central cheia) está adiado, só criamos agora o que a authorship
precisa, em `.rhaxis.yaml` ou via flags de CLI:

```yaml
# .rhaxis.yaml (opcional)
authorship:
  enabled: true                  # default: false; CLI --authorship ativa também
  store_email: hash              # plain | hash | none
  github_login: false
  ignore_revs: .git-blame-ignore-revs
  teams_file: .rhaxis-teams.yaml # default: .rhaxis-teams.yaml se existir
```

Equivalentes em CLI:

```
rhaxis extract --authorship --store-email=hash
```

**`store_email: hash`** salva `sha256(email)` em vez do email cru (LGPD). O hash preserva
identidade entre commits do mesmo autor sem expor PII.

**`ignore_revs`** lista SHAs de commits de formatação/lint que **não** devem gerar
`LAST_MODIFIED` nem alterar `OWNS`. É o mesmo arquivo que `git blame --ignore-revs-file`
consome.

### 2.6 Derivação de edges (fluxo)

Durante `rhaxis extract`:

```
1. Rodar extração estática normal → grafo atual.
2. SE authorship ativa:
   a. git log <range>     →  cria Commit + Person + AUTHORED_BY/COMMITTED_BY
   b. git blame -w -M -C <rev> <arquivo> para cada arquivo tocado
      → por linha, resolve Loc→declaração mais interna, soma linhas por autor
      → emite OWNS {source:"blame", share: lines/total} Person → declaração
   c. Primeiro commit de cada declaração → emite CREATED Person → declaração
   d. Último commit body|signature de cada declaração → LAST_MODIFIED Person → declaração
   e. Se existe CODEOWNERS ou .rhaxis-teams.yaml:
      - cria Team + MEMBER_OF
      - para cada pattern que casa com arquivo:
          emite OWNS {source:"codeowners"} Team → File (ou declaração via contenção)
```

Blame roda só nos arquivos tocados no `<range>` solicitado. Para extração inicial, roda em
todos os arquivos (uma vez).

### 2.7 Novos pacotes

```
extractor/authorship/
  authorship.go       // orquestração dos passos 2.6
  commit.go           // git log → Commit
  person.go           // normalização, mailmap, hash
  blame.go            // git blame → OWNS por declaração (usa Loc index)
  teams.go            // CODEOWNERS + .rhaxis-teams.yaml → Team + OWNS
  config.go           // leitura da config mínima
```

### 2.8 Aceite

- Fixture de repo com 3 commits: 2 autores diferentes, um deles tocando duas funções.
- Grafo tem 2 Person, 3 Commit, 1 Team (via `.rhaxis-teams.yaml` da fixture).
- `AUTHORED_BY` e `CHANGED` presentes para os 3 commits.
- `OWNS {source:"blame"}` em cada função, com share > 0.
- `OWNS {source:"codeowners"}` em arquivo coberto pelo CODEOWNERS.
- `.git-blame-ignore-revs` com um SHA fictício: aquele commit não aparece em `LAST_MODIFIED`.

---

## 3. Link cross-service por inferência

### 3.1 Princípio

Rhaxis **não exige** `.rhaxis-services.yaml`. O link entre serviços é inferido olhando o
grafo já extraído: hosts literais em `fetch`/`axios`, nomes de env vars, nomes de tópicos,
e endpoints expostos. Em cada extração nova, o linker tenta ligar essa app ao resto.

Regra de ouro:

- **Match único e evidente** → cria a edge automaticamente, `Resolution=exact` ou
  `Resolution=inferred` conforme o caso.
- **Múltiplos candidatos** → cria a edge marcada como `Resolution=ambiguous` e registra um
  item de revisão (seção 3.5). Humano confirma depois; sem bloquear a extração.
- **Nenhum candidato** → silêncio total. Mantém só o `CALLS External` original. Não gera
  pergunta.

Isso garante que (a) rodar `rhaxis extract` em um projeto novo nunca pede config extra,
(b) o grafo cresce sozinho conforme mais serviços entram, (c) o humano só é incomodado
quando há genuína ambiguidade.

### 3.2 Nodes e edges novos

```go
// entity/node.go
type Topic struct {
    Base
    Name   string
    Broker string            // kafka | rabbitmq | sns | sqs | redis | unknown
}

// entity/edge.go
RequestsEdge EdgeType = "REQUESTS"     // Function|Call → Endpoint
ProducesEdge EdgeType = "PRODUCES"     // Function → Topic
ConsumesEdge EdgeType = "CONSUMES"     // Function → Topic
```

**Decisão:** `Queue` virou `Topic` com `Broker=sqs|rabbitmq` para não duplicar. Nome é a
chave; broker é property.

### 3.3 Sinais que cada Application expõe

Durante a extração normal, a Application acumula sinais que servem de alvo para o linker.
Nada novo pro usuário: tudo é inferido do código já parseado.

Guardados como properties na `Application`:

```go
type Application struct {
    Base
    Name     string
    Key      string
    // novos:
    Endpoints       []EndpointSignal     // path + method expostos
    EnvVarsRead     []string              // process.env.X encontrados
    TopicsProduced  []string              // literais em kafka.produce / sns.publish
    TopicsConsumed  []string              // literais em kafka.consume / handlers de SQS
    HostCandidates  []string              // domínios/hosts que o próprio código menciona
}
```

Esses sinais já existem como nodes/edges no grafo (`Endpoint`, `CallsEdge` para
`process.env`, etc.). O novo é denormalizar na `Application` pra lookup O(1) no linker.

### 3.4 Resolvedores (ordem de tentativa)

Rodam no comando `rhaxis link-services`, **depois** de cada extração nova (ou on-demand).

Para cada `Call -CALLS-> External` ou similar que ainda não tem `REQUESTS`:

**R1. URL literal.**
- Entrada: argumento é string literal como `"http://users:8080/users/" + id` ou template
  `` `http://users:8080/users/${id}` ``.
- Extrai host (`users:8080`) e path (`/users/:id`).
- Para cada Application A no grafo:
  - Score 2 se `A.HostCandidates` contém o host OU `A.Name/Key` casa o host (prefix).
  - Score 1 se algum `A.Endpoints` casa o path.
  - Score 0 caso contrário.
- Candidatos = apps com score > 0, ordenados por score desc.
- 1 candidato → `REQUESTS`, `Resolution=exact` se score=3, `inferred` caso contrário.
- 2+ candidatos → `REQUESTS` para o top, `Resolution=ambiguous`, registra item de revisão
  listando todos.
- 0 candidatos → silêncio.

**R2. Env var.**
- Entrada: argumento é `process.env.X` (ou `process.env.X + "/path"`).
- Para cada Application A:
  - Score 2 se `A.EnvVarsRead` contém `X` (a própria app lê a mesma env — raro mas sinal
    forte quando acontece via infra compartilhada; só vale pra apps "irmãs").
  - Score 1 se o prefixo de `X` casa `A.Name/Key` (`USERS_SERVICE_URL` → app `users*`).
- 1 candidato → `REQUESTS`, `Resolution=inferred`.
- 2+ → ambiguous.
- 0 → silêncio.

**R3. Fila/tópico.**
- Entrada: literal do primeiro argumento de `kafka.produce('X', ...)`,
  `rabbit.publish('X', ...)`, `sqs.sendMessage({QueueUrl..., ...})`, etc.
- Cria/reaproveita `Topic{Name:X, Broker:detectado}`.
- Emite `PRODUCES` ou `CONSUMES` da função → Topic.
- **Match entre serviços é automático via Topic compartilhado**: se outra app já tem
  `PRODUCES Topic{X}` e esta ganha `CONSUMES Topic{X}`, o link está feito pelo próprio
  node Topic. Nenhuma edge direta entre apps, mas as queries conseguem navegar.

**R4. SDK alias.** Adiado (depende de library extraction).

### 3.5 Itens de revisão

Quando um resolver gera `Resolution=ambiguous`, grava em um node auxiliar:

```go
type LinkReview struct {
    Base
    CallID      string       // ID do Call ou Function de origem
    EdgeType    EdgeType     // REQUESTS | PRODUCES | CONSUMES
    ChosenID    string       // alvo escolhido (top score)
    Candidates  []LinkCandidate   // todos os candidatos com score
    Hint        string       // "fetch('http://users:8080/x')" etc
    Status      string       // "pending" | "confirmed" | "rejected" | "overridden"
}

type LinkCandidate struct {
    TargetID string
    Score    int
    Why      string
}
```

Comando pra revisar:

```
rhaxis link-services review
  # lista todos os LinkReview com Status=pending
  # formato: callsite → chosen (score X)   /   candidates: a (3), b (2)

rhaxis link-services confirm <reviewID>
  # muda Status=confirmed; edge original fica como está

rhaxis link-services override <reviewID> <targetID>
  # troca o alvo da edge; Status=overridden

rhaxis link-services reject <reviewID>
  # remove a edge; Status=rejected
```

Rhaxis **nunca** prompta durante `extract` — isso quebra automação. A revisão é sempre
assíncrona.

### 3.6 Comando principal

```
rhaxis link-services
  # roda os 3 resolvedores em todas as apps do grafo
  # idempotente: edges antigas de REQUESTS/PRODUCES/CONSUMES são recriadas
  # imprime no fim: "X exact, Y inferred, Z ambiguous (use 'review' pra ver)"

rhaxis link-services audit
  # lista Calls para External que não foram ligados a nenhuma app conhecida
  # formato: (service, call_location, callee_text) — ajuda a identificar APIs 3rd-party
```

### 3.7 Novo pacote

```
extractor/crosservice/
  signals.go          // denormaliza Endpoint/env/tópicos na Application
  linker.go           // orquestra R1..R3
  url_resolver.go     // R1
  env_resolver.go     // R2
  topic_resolver.go   // R3
  review.go           // LinkReview CRUD + CLI subcommands
  cmd.go              // link-services + review + confirm + override + reject + audit
```

### 3.8 Aceite

- 2 fixtures de serviço:
  - Serviço B expõe `GET /users/:id` (Endpoint extraído).
  - Serviço A tem `fetch('http://b:8080/users/' + id)`.
- Após `extract A`, `extract B`, `link-services`:
  - Existe `REQUESTS A.callerFn → B.getUser` com `Resolution=exact` (host casa B.Name,
    path casa Endpoint).
- Variante com `process.env.B_URL + '/users/'`:
  - Linker reconhece prefixo `B_*` → casa com app `b` → `REQUESTS` inferred.
- Serviço C publica em `orders.created` e D consome:
  - `PRODUCES C.publishFn → Topic{orders.created}`.
  - `CONSUMES D.handlerFn → Topic{orders.created}`.
  - Mesmo Topic node liga os dois.
- 2 apps com nomes similares (`users` e `users-admin`) com hosts semelhantes:
  - `REQUESTS` com `Resolution=ambiguous`; `link-services review` lista os dois.
- Call para `https://api.stripe.com/v1/charges`:
  - Nenhum match. Nenhum review criado. `audit` mostra na lista pro humano decidir.

---

## 4. Pipeline integrada

Novo fluxo do `rhaxis extract`:

```
1. source → parser → linker (como hoje)
2. acumula signals na Application (endpoints, envs, tópicos, hosts)
3. SE authorship ativa:
     git log + blame → Person, Team, Commit, AUTHORED_BY, CHANGED, OWNS, CREATED, LAST_MODIFIED
4. persiste no Neo4j (ou JSON)

Separado:
rhaxis link-services
   roda depois que N apps estão no banco
   emite REQUESTS, PRODUCES, CONSUMES + LinkReview para ambíguos
```

---

## 5. Entregáveis por sprint

Cada sprint é independente e entrega valor sozinho.

### Sprint 1 — Git source
- Pacote `extractor/source/git/` com `Walk` e `ReadFile`.
- CLI `-rev` flag.
- Teste de equivalência com `source/local`.
- Testes com repo temporário.

**Entrega:** extrai qualquer commit sem checkout.

### Sprint 2 — Autoria mínima: Commit + Person + CHANGED
- Pacote `extractor/authorship/` só com `commit.go` + `person.go` + `config.go`.
- Nodes `Commit`, `Person`. Edges `AUTHORED_BY`, `COMMITTED_BY`, `CHANGED`.
- Normalização `.mailmap`, hash opcional de email.
- Primeiro sem blame (sem `OWNS`, `CREATED`, `LAST_MODIFIED`).
- Config mínima lida de `.rhaxis.yaml` seção `authorship` ou flags de CLI.

**Entrega:** "quem mudou quais funções nesses commits".

### Sprint 3 — Blame (só Person, sem Teams)
- `authorship/blame.go` → `OWNS {kind:"blame", share}`, `CREATED`, `LAST_MODIFIED` com `Person`.
- Cada linha → declaração mais interna que a contém. Lines de uma declaração são
  rateadas por autor (via email normalizado) → `share = lines/total`.
- `CREATED` = autor do commit mais antigo que tocou alguma linha da declaração;
  `LAST_MODIFIED` = autor do commit mais recente.
- Teams, `MEMBER_OF`, CODEOWNERS e `.rhaxis-teams.yaml` saem **do escopo** deste plano.
  Ficam como "pode voltar depois se preciso".

**Entrega:** "quem é dono desta função/classe" — rankeado por share.

### Sprint 4 — Signals + cross-service URL/env
- Novas properties na `Application` (`Endpoints`, `EnvVarsRead`, `HostCandidates`, etc.).
- Pacote `extractor/crosservice/` com R1 (URL) e R2 (env).
- Nodes `Topic` (sem resolver ainda) e `LinkReview`.
- Comando `rhaxis link-services` + `review` + `confirm` + `override` + `reject` + `audit`.

**Entrega:** chamadas HTTP entre serviços ligadas automaticamente nos casos literais.

### Sprint 5 — Cross-service tópicos
- R3: `topic_resolver.go` — extrai literais de `kafka.produce`, `rabbit.publish`, `sqs.sendMessage`.
- Emite `PRODUCES` e `CONSUMES` via Topic compartilhado.

**Entrega:** cobertura de fluxos assíncronos no grafo.

---

## 6. Riscos específicos

| Risco | Mitigação |
|---|---|
| `git cat-file --batch` com SHAs inválidos | Validar que `ls-tree` listou antes; stdout do batch sinaliza "missing" por SHA |
| `.mailmap` ausente, mesmo dev vira dois Person | Comando `rhaxis people merge <id1> <id2>` futuro; por enquanto doc orienta ter mailmap |
| CODEOWNERS com sintaxe não suportada | Parser estrito; ignora com warning linhas que não entende |
| `fetch(baseUrl + path)` com baseUrl construído em runtime | R1 degrada para sem host; se path casar Endpoint único, ainda liga `inferred`. Senão silêncio. |
| Dois serviços com nomes parecidos (`users`, `users-admin`) | Ambiguous resolve com `LinkReview`; humano confirma uma vez. |
| Call para APIs 3rd-party (Stripe, Twilio) | Zero match = silêncio; `audit` lista pra humano decidir se cria External label |
| Topic com mesmo nome em brokers diferentes | `Topic.Name` é a chave; `Broker` é property; se brokers conflitam, cria dois Topics distintos |
| Reextração substitui edges REQUESTS mas apaga LinkReview confirmed | `LinkReview.Status=confirmed/overridden` é persistido; linker respeita override na próxima run |
| Signals na Application incham o node | Limites: top 50 endpoints, top 20 env vars, top 20 topics; o resto fica só nos nodes originais |

---

## 7. Novas dependências

| Dependência | Para quê | Alternativa |
|---|---|---|
| Nenhum binding Go novo para git | Usar `os/exec` com `git` CLI | `go-git`, mas módulo pesado e proxy do Go falhou em WSL — ficou de fora |
| `gopkg.in/yaml.v3` | `.rhaxis.yaml` e `.rhaxis-teams.yaml` (ambos opcionais) | Verificar se já está no `go.mod` |

---

## 8. O que fica para depois (fora deste plano)

- Config central `.rhaxis.yaml` com schema completo (hoje: só authorship mínima).
- Teams (CODEOWNERS, `.rhaxis-teams.yaml`), `MEMBER_OF`, `OWNS` de origem config.
- Extração de libraries externas/internas com dedup e `max_depth`.
- SDK resolver (R4) para cross-service — depende de libraries.
- Versionamento completo com `FunctionVersion`/`HAS_VERSION`/`PRODUCED` e validade temporal
  nas edges (`change-analysis-plan.md` 11b–11c).
- Diff semântico, impacto, suspeitos (`change-analysis-plan.md` 5–7).
- CI/CD, PR comment, deploy marking (`change-analysis-plan.md` 12).
- Telemetria / `rhaxis explain -trace` (`multi-service-telemetry-plan.md` partes 1 e 4).
- gRPC via `.proto`.
- Linguagens além de JS/TS.
