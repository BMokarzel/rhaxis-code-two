# Plano: telemetria, multi-serviço, libs compartilhadas e root-cause via tracer

Complementa `change-analysis-plan.md`. Divisão de escopo:

| Tema | Documento |
|---|---|
| Extração de qualquer commit, cache por blob, diff semântico, impacto, suspeitos por mudança | `change-analysis-plan.md` etapas 1–10 |
| Autoria e versionamento (nodes `Commit`, `Person`, `FunctionVersion`, `AUTHORED_BY`, `OWNS`) | `change-analysis-plan.md` etapa 11 |
| CI/CD, PRs, deploys, incidentes por stack trace | `change-analysis-plan.md` etapa 12 |
| **Telemetria (logs/traces/spans)** | este doc, parte 1 |
| **Multi-serviço (chamadas entre apps)** | este doc, parte 2 |
| **Libs externas e internas de empresa** | este doc, parte 3 |
| **Root-cause determinístico a partir de tracer id** | este doc, parte 4 |

Dependências:
- Parte 1 depende de `change-analysis` etapa 4 (linha → declaração).
- Parte 2 depende do M2 (nodes `Endpoint`, `HttpParam` — já existem) e da parte 3 (resolver "esse External é meu").
- Parte 3 é independente, dá pra começar junto.
- Parte 4 depende das partes 1 e 2 e do histórico (etapa 11).

---

## Parte 1 — Integração com telemetria externa

### 1.1 Princípio

**Rhaxis não é uma plataforma de telemetria.** Não ingere, não armazena, não agrega eventos. O cliente já tem Honeycomb/Datadog/Grafana/Splunk/Sentry rodando; rhaxis **consulta** esses sistemas sob demanda e **recebe webhooks** quando acontece algo relevante.

Vantagens dessa escolha:
- Zero storage próprio de evento (nada de LGPD/compliance extra, nada de bucket, nada de Kafka).
- Zero duplicação de dado (o backend do cliente continua sendo a fonte da verdade).
- Zero objeção de "mais uma ferramenta pra manter" na venda.
- Rhaxis fica focado no que só ele faz: **grafo de código + correlação com runtime**.

> **Ingestão/mirror próprios (ex.: ser um OTLP receiver com storage) ficam fora do escopo.** Se um dia um cliente pedir análise longitudinal fora da retenção do backend principal, isso vira **outro serviço** (ex.: `rhaxis-collector`), independente do core.

### 1.2 Dois modos de interação

**Push (webhook).** Backend avisa o rhaxis quando algo relevante acontece.
- Fontes típicas: Sentry (nova exceção), Grafana Alerting (alerta disparou), PagerDuty (incidente).
- Payload mínimo que resolve: `trace_id` **ou** stacktrace **ou** `(service, file:line)`.
- Rhaxis roda `explain` automaticamente, comenta no incidente/issue com o resultado.
- Configuração: um endpoint HTTP por fonte, autenticação por token.

**Pull (query sob demanda).** Rhaxis consulta o backend quando o usuário pede.
- Dispara em: `rhaxis explain -trace T`, CI do PR enriquecendo o relatório, consulta ad-hoc.
- Rhaxis conhece a interface `TelemetryProvider` e pergunta o que precisa.
- Cache local de curta duração (minutos) pra evitar custo repetido de query.

Modo híbrido é o esperado em produção: push dispara a análise, pull enriquece com contexto adicional.

### 1.3 Interface `TelemetryProvider`

Única superfície que o resto do rhaxis conhece de telemetria. Qualquer backend que implemente isso é plugável.

```go
type TelemetryProvider interface {
    Name() string
    Capabilities() Capabilities

    // Dado um trace_id, devolve a árvore de spans + logs + erros associados.
    // Usado pelo rhaxis explain -trace T.
    GetTrace(ctx context.Context, traceID string) (*Trace, error)

    // Para cada (service, file, function) devolve métricas agregadas na janela.
    // Usado por risk score de deploy, pontos quentes, enriquecimento do PR.
    QueryMetrics(ctx context.Context, keys []NodeKey, window Window) (map[NodeKey]Metrics, error)

    // Erros recentes nos nodes informados. Usado por "esta função já falha em prod?".
    RecentErrors(ctx context.Context, keys []NodeKey, since time.Time, limit int) ([]ErrorEvent, error)
}

type NodeKey struct {
    Service  string  // bate com Application.key do grafo
    File     string  // path relativo ao root do repo (mesmo que File.Path)
    Function string
    Line     int     // opcional
}

type Capabilities struct {
    Traces, Metrics, Errors bool
    HasCodeAttribution bool   // backend expõe code.function/filepath/lineno? Se não, degrada.
    QueryCostNote      string // dica pra o rhaxis cachear mais agressivamente
}

type Trace struct {
    ID       string
    Service  string
    Spans    []Span   // já com parent_span_id resolvido
    Logs     []LogEvent
    Errors   []ErrorEvent
}

type Span struct {
    ID, ParentID, Name, Service string
    Kind                        string  // server, client, internal, producer, consumer
    StartedAt                   time.Time
    DurationMs                  float64
    Status                      string  // ok, error, unset
    Attrs                       map[string]string  // inclui code.function, code.filepath, code.lineno quando houver
}
```

**Degradação graciosa:** quando `Capabilities` não cobre algo, o chamador adapta — ex.: sem `QueryMetrics`, o relatório de PR não lista error_rate, mas o impacto estático continua saindo.

### 1.4 Mapeamento `(Span|Log) → nodeID`

É aqui que o grafo entra: dado um evento do backend, achar o node correspondente no Neo4j. **Isso vive no rhaxis, não no adapter.**

Prioridade de resolução (mais confiável primeiro):

1. **`code.function` + `code.filepath` + `code.lineno`** (OTEL semantic conv): resolve direto via `Loc → nodeID` da etapa 4 do `change-analysis`. Confiança `exact`.
2. **Stacktrace de erro**: cada frame vira `file:line`, resolve do mais interno pra fora. Confiança `exact` nos frames do código do cliente, `external` em frames de libs.
3. **`http.route` + `http.method` + `service.name`**: casa com `Endpoint` do grafo. Confiança `exact`.
4. **`span.name` convencional** (ex.: `UsersService.findOne`): procura por nome qualificado. Confiança `inferred`.
5. **Fallback**: casa pelo arquivo e nome da operação. Confiança `name_only`.

O resolver **tem que saber qual commit estava em produção no momento do evento** — path pode ter mudado. Resolve via `Deployment -DEPLOYS-> Commit` (etapa 12.6 do change-analysis). Sem `Deployment` registrado, assume HEAD com warning.

### 1.5 Adapters prioritários

Cada adapter é um pacote isolado implementando `TelemetryProvider`. Pode ser aberto à comunidade.

| # | Adapter | Pacote | Modo | Esforço | Motivo da prioridade |
|---|---|---|---|---|---|
| 1 | OpenTelemetry (via query de qualquer backend OTLP-compatível) | `telemetry/otel` | Pull | Médio | Semantic conv dá `code.*` sem heurística |
| 2 | Sentry/GlitchTip | `telemetry/sentry` | Push + Pull | Baixo | Caso de uso #1: incidente → root-cause |
| 3 | Grafana (Loki + Tempo + Mimir) | `telemetry/grafana` | Pull + Push (alerting) | Médio | Stack OSS dominante |
| 4 | Honeycomb | `telemetry/honeycomb` | Pull + Push (triggers) | Médio | Query API boa, público de SRE engenheirado |
| 5 | Datadog | `telemetry/datadog` | Pull + Push (monitors) | Alto | Dominante enterprise |
| 6 | New Relic | `telemetry/newrelic` | Pull | Médio | NRQL é SQL-like |
| 7 | Splunk | `telemetry/splunk` | Pull | Alto | Legado enterprise, SPL complexo |
| 8 | CloudWatch + X-Ray | `telemetry/aws` | Pull | Médio | Shops AWS-puros |

Rhaxis entrega **1, 2, 3** no core. Resto fica como adapter opcional (OSS no mesmo repo ou plugin externo).

### 1.6 Arquitetura no código

```
extractor/telemetry/
  provider.go              // interface TelemetryProvider + tipos
  resolver.go              // (Span|Log|StackFrame) → NodeID, usando Loc index
  registry.go              // lookup por nome: "sentry" → sentry.New(config)
  cache.go                 // cache de respostas (TTL curto, chaveado por query)
  otel/                    // adapter 1
  sentry/                  // adapter 2
  grafana/                 // adapter 3
  honeycomb/               // futuro
  ...

extractor/cmd/
  explain.go               // rhaxis explain -trace T -provider sentry
  ci_pr.go                 // enriquece PR com dados de telemetria (opcional)

extractor/webhook/         // servidor HTTP que recebe push
  server.go
  sentry_handler.go        // valida assinatura Sentry, dispara explain
  grafana_handler.go
```

**Configuração.** Rhaxis central (modo B do CI) tem um arquivo:

```yaml
# .rhaxis-telemetry.yaml
providers:
  - name: sentry-prod
    kind: sentry
    base_url: https://sentry.io
    org: empresa
    token_env: SENTRY_TOKEN
    services: [users-api, billing-api]       # quais apps esse provider cobre
  - name: honeycomb-prod
    kind: honeycomb
    api_key_env: HONEYCOMB_KEY
    dataset: prod
    services: [users-api, billing-api, orders-worker]

webhooks:
  - path: /webhook/sentry
    provider: sentry-prod
    secret_env: SENTRY_WEBHOOK_SECRET
    on_event: issue.created
    action: explain_and_comment              # roda explain, posta no GitHub
```

Mesmo serviço pode ter múltiplos providers (ex.: Sentry pra erros, Honeycomb pra traces). Rhaxis consulta todos; cada um responde o que sabe (`Capabilities`).

### 1.7 Riscos

| Risco | Mitigação |
|---|---|
| Backend lento → `explain` lento | Cache local; timeout por provider; degradação (devolve só o estático se telemetria demorar) |
| Query cobra por volume (Datadog) | Cache agressivo, cap de queries por `explain`, agendar jobs em janela baixa |
| Rate limit do backend | Backoff + fila; configuração de `max_qps` por provider |
| Sem `code.function` nos spans | Resolver cai pra heurística 4-5; marca confiança baixa; sugere ao time instrumentar |
| Mudança de API do backend | Adapter é um pacote isolado; versão fixa testada; CI roda smoke test contra API real |
| Múltiplos providers divergem (DD diz erro, Honeycomb não) | Mostrar lado a lado; não tentar reconciliar automaticamente |
| Push duplicado (webhook retentado) | Dedup por `(trace_id, event_id)` com TTL curto |

---

## Parte 2 — Multi-serviço (link entre microservices)

### 2.1 Objetivo

Transformar `Call -CALLS-> External{Member="/users/:id"}` do serviço A em `Call -REQUESTS-> Endpoint` do serviço B. Idem para mensageria, gRPC, filas. Hoje o grafo acaba na fronteira do processo.

### 2.2 Modelo

```
(:Endpoint {path, method, service})                             já existe
(:Topic   {name, broker})                                       novo
(:Queue   {name, broker})                                       novo
(:RPCMethod {service, method})                                  novo

// edges cross-service (resolvidas pelo linker depois de todas as apps extraídas):
(Call|Function)-[:REQUESTS   {confidence}]->(Endpoint)
(Call|Function)-[:PRODUCES   {confidence}]->(Topic|Queue)
(Function     )-[:CONSUMES   {confidence}]->(Topic|Queue)
(Call|Function)-[:INVOKES_RPC{confidence}]->(RPCMethod)
```

### 2.3 Manifesto de serviços (feedback humano)

Precisa de uma fonte de verdade: "quem é dono de que path, em que host, versão da API". Sem isso, URL=string e nada liga.

**Arquivo:** `.rhaxis-services.yaml` (no root do monorepo, ou um por repo cadastrado numa pasta central). Reindexado toda extração.

```yaml
version: 1
services:
  - key: users-api
    repo: github.com/empresa/users-api
    base_urls: [https://users.prod.internal, http://users:8080]
    aliases: [USERS_SERVICE_URL, @empresa/users-client]   # env vars ou pacotes que resolvem pra ele
    openapi: docs/openapi.yaml                             # opcional, resolve path params
  - key: billing-api
    base_urls: [https://billing.prod.internal]
  - key: orders-worker
    topics_consumed: [orders.created]
    topics_produced: [orders.enriched]
```

- `aliases` é a parte com feedback humano: num SDK como `@empresa/users-client`, `client.getUser(id)` vira `REQUESTS -> users-api:GET /users/:id` só se o manifesto disser isso.
- Env vars no `aliases` capturam `fetch(process.env.USERS_SERVICE_URL + '/users/' + id)`.
- `openapi` opcional dá tipo de response e lista de endpoints esperados (serve pra validar que `/users/:id` existe no destino).

### 2.4 Resolvedores

Etapa pós-linker, roda depois que **todas as apps do universo** estão no grafo:

1. **URL literal** → `fetch('https://users.prod/users/' + id)`:
   - Extrair base + path do argumento (parse de template string e concat).
   - Match com `base_urls` do manifesto → serviço.
   - Match path contra `Endpoint.path` do serviço (resolve `:id`, `{id}`).
   - Confidence: `exact` se bate base + path.
2. **Env var** → `fetch(process.env.USERS_SERVICE_URL + '/users')`:
   - Resolver `process.env.X` via `aliases` do manifesto.
   - Confidence: `exact` se env var listada; `name_only` senão.
3. **SDK/cliente gerado** → `usersClient.getUser(id)`:
   - Via manifesto `aliases: [@empresa/users-client]`, o pacote é marcado como alias do serviço `users-api`.
   - Mapa de método→endpoint vem do próprio OpenAPI do destino (`getUser` → `GET /users/:id` por `operationId`).
   - Confidence: `exact` se OpenAPI casa; `inferred` se só o nome do pacote bate.
4. **Fila/tópico** → `kafka.produce('orders.created', payload)`:
   - Extrair string literal do primeiro arg.
   - Match com `topics_produced`/`topics_consumed` do manifesto.
   - Confidence: `exact` quando literal.
5. **gRPC** → `client.CreateOrder({...})`:
   - Precisa extrair `.proto` (parser novo, fora do escopo inicial). Alternativa: manifesto declara `rpc_methods: [CreateOrder → orders-api:CreateOrder]`.

### 2.5 Pipeline

```
rhaxis extract -app users-api   →  grafo A no Neo4j
rhaxis extract -app billing-api →  grafo B no Neo4j
rhaxis extract -app orders-worker →  grafo C
rhaxis link-services -manifest services.yaml   ← cria REQUESTS, PRODUCES, CONSUMES
```

`link-services` é separado porque não exige reextração dos repos quando o manifesto muda — é só reescrever as edges cross-service.

### 2.6 Casos de uso

```cypher
// um PR em users-api removeu GET /users/:id. Quem quebra?
MATCH (c:Caller)-[:REQUESTS]->(e:Endpoint {service: 'users-api', path: '/users/:id'})
RETURN c.service, c.id

// cadeia completa de uma request
MATCH path = (e:Endpoint {path: '/orders'})-[:HANDLED_BY]->()-[:INVOKES|REQUESTS|PRODUCES*0..6]->(leaf)
RETURN path LIMIT 5

// serviços órfãos (ninguém chama)
MATCH (s)-[:HANDLED_BY]->(e:Endpoint)
WHERE NOT ((:Function)-[:REQUESTS]->(e))
RETURN e
```

### 2.7 Riscos

| Risco | Mitigação |
|---|---|
| URL dinâmica demais (concat de várias partes) | Marcar `name_only` quando não dá pra reduzir a literal; manifesto permite declarar manualmente |
| Serviço fora do grafo (terceiro, legado) | Endpoint cadastrado no manifesto com `external: true` cria `Endpoint` sem código por trás |
| Monorepo com N serviços no mesmo repo | Cada aplicação é extraída isoladamente (`app` key); manifesto resolve após |
| OpenAPI defasado | Confidence cai pra `inferred`; CI pode avisar quando SDK não bate com spec |
| Manifesto desatualizado | Comando `rhaxis services audit` lista `REQUESTS -> External` que poderiam ter virado endpoint, pedindo entrada humana |

---

## Parte 3 — Libraries (externas e internas)

### 3.1 Objetivo

Hoje `pkg:lodash` é um `External` opaco. Mesma dependência extraída N vezes nos N projetos que a usam, sem internals. Para libs internas da empresa (`@empresa/users-client`, `@empresa/utils`), isso é desperdício e perda de info: a lib é código próprio, só mora em outro repo.

Objetivos:
1. **Deduplicar extração** da mesma versão da mesma lib.
2. **Expor a estrutura interna** da lib (funções, tipos) para análise de impacto.
3. **Ligar consumidor → implementação da lib** (`REQUESTS` análogo pra bibliotecas).

### 3.2 Modelo

```
(:Library {key: "lib:lodash@4.17.21", name, version, source: "npm|git|local",
           sha256: "<hash do tarball>", extracted_at})
   -[:HAS_APPLICATION]-> (:Application {key: "app:lib:lodash@4.17.21"})

// grafo da lib fica dentro dessa app isolada; função normal, apenas
(Library)-[:HAS_FUNCTION|HAS_CLASS|HAS_TYPE]->(...)

// consumidores apontam pra lib:
(:Package {key: "pkg:lodash"})-[:RESOLVES_TO {version_range: "^4.17.0", pinned: "4.17.21"}]->(:Library)
(:Function {service: 'users-api'})-[:USES_LIBRARY_FN]->(:Function {library: "lib:lodash@4.17.21"})
```

### 3.3 Deduplicação

**Chave de deduplicação:** `(name, version, sha256_do_tarball)`.

- SHA do tarball (não do source) protege contra "mesma versão publicada diferente" (raro mas acontece).
- Para libs internas em git: `(repo_url, commit_sha)`.
- Para libs no mesmo monorepo (`workspace:*`): extraída junto com a app, aponta pra ela por path.

**Fluxo:**

```
rhaxis extract -app users-api
  → lê package-lock.json
  → para cada dependency:
      key = (name, version, sha256)
      SE Library já existe no Neo4j → só cria RESOLVES_TO
      SENÃO:
        baixa tarball (npm registry ou cache local)
        extrai como se fosse app comum (app:lib:name@version)
        cria Library + HAS_APPLICATION
        cria RESOLVES_TO
```

Resultado: 50 serviços que usam `lodash@4.17.21` compartilham **uma** extração dela.

### 3.4 Libs internas

Mesma mecânica, mas:
- Fonte: registry privado da empresa (npm interno) ou git direto.
- Se o repo da lib também é extraído standalone (porque ela tem CI próprio), as duas aplicações convivem: `app:@empresa/users-client` (standalone) e `app:lib:@empresa/users-client@2.3.0` (publicado). O manifesto da parte 2 declara a equivalência: `aliases: [@empresa/users-client → users-client]`.

### 3.5 Impacto cross-lib

```cypher
// quem usa esta função da lib
MATCH (consumer)-[:USES_LIBRARY_FN]->(f:Function {id: 'lib:lodash@4.17.21:...#debounce'})
RETURN consumer.service, count(*) GROUP BY consumer.service

// bump de versão: o que muda da 4.17.20 pra 4.17.21?
MATCH (old:Library {key: 'lib:lodash@4.17.20'})-[:HAS_APPLICATION]->(:Application)-[*]->(f1:Function),
      (new:Library {key: 'lib:lodash@4.17.21'})-[:HAS_APPLICATION]->(:Application)-[*]->(f2:Function)
WHERE f1.name = f2.name AND f1.bodyHash <> f2.bodyHash
RETURN f1.id, f1.bodyHash, f2.bodyHash
```

Isso só funciona se o parser consegue ler o código publicado — tipicamente é possível (`.d.ts` + sources em `dist/`), mas às vezes só minified. Caso minified: extração degradada (nomes de alto nível preservados pelo `.d.ts`, corpos opacos).

### 3.6 Configuração

```yaml
# .rhaxis-libs.yaml (no servidor rhaxis central, não por repo)
registries:
  - name: npm-public
    kind: npm
    url: https://registry.npmjs.org
  - name: npm-internal
    kind: npm
    url: https://npm.empresa.internal
    scopes: ["@empresa"]
  - name: company-git
    kind: git
    url: https://git.empresa.internal
    scopes: ["@legacy"]            # pacotes que não estão em registry, só em git
cache:
  path: /var/rhaxis/libs
  max_size_gb: 100
extraction:
  skip_patterns: ["**/dist/**/*.min.js"]    # já minificado, não vale a pena
  only_published_sources: true              # ignora test, examples da lib
```

### 3.7 Riscos

| Risco | Mitigação |
|---|---|
| Lib minificada sem source map | Grava `Library.quality="minified"`; análise degradada mas aceita |
| Grafo infla com milhares de libs | Extração sob demanda: só quando algum consumidor existe no Neo4j; GC remove libs sem `RESOLVES_TO` |
| Transitividade explode (dep de dep de dep) | Profundidade máxima configurável; libs puras (lodash → nenhuma) são baratas, framework (react) são caras |
| Lib privada muda sem bump de versão | SHA do tarball detecta mesmo com versão igual; cria `Library` nova com warning |

---

## Parte 4 — Root-cause via tracer id: até onde dá pra ser determinístico

### 4.1 Pergunta honesta

> Dado uma lista de logs com o mesmo `trace_id`, o rhaxis consegue dizer **com certeza** onde o bug está e por quê?

**Resposta curta:** não 100% determinístico, mas **drasticamente mais barato que investigação manual**. Quanto mais sinal (OTEL com `code.function`, stacktrace, histórico de deploy, mudanças recentes), mais a saída se aproxima de determinística.

### 4.2 O que o grafo entrega e onde para

Classificando por grau de certeza:

| Pergunta | Determinismo | Depende de |
|---|---|---|
| **Em qual função o erro foi lançado** | Determinístico | stacktrace OU `code.function` do OTEL — mapeia direto via Loc→node |
| **Qual sequência de funções foi executada** | Determinístico | OTEL spans com `code.function` cobrindo cada chamada. Sem OTEL, só os frames da stack. |
| **Qual valor concreto causou o erro** | Não | É dado runtime; grafo não tem valores. O log tem (se logou), fora disso é opaco. |
| **De onde o valor problemático veio** | Semi-determinístico | `FLOWS_TO` reverso mostra os caminhos possíveis. Se vários, grafo lista N; log/trace precisa confirmar qual. |
| **Qual mudança recente introduziu** | Determinístico | Cruzamento com histórico (etapa 11): changesets no intervalo `deploy anterior..atual` ∩ cone do sintoma. |
| **Se o bug é lógico vs. de ambiente/dado** | Não | Grafo só vê código. Config/dado/latência de dep externa escapam. |

### 4.3 Procedimento proposto

Dado: `trace_id = T`, serviço `S`, timestamp `t`, lista de providers configurados.

```
1. Para cada provider com Capability.Traces: provider.GetTrace(T).
   Consolida spans + logs + erros da trace (todos vindos do backend do cliente;
   rhaxis não guarda).
2. Identifica o Deployment ativo em (S, t) → Commit C.
   (Via Deployment -DEPLOYS-> Commit, etapa 12.6 do change-analysis.)
3. Materializa a "slice de execução": conjunto de nodeIDs atingidos pela trace.
     Spans com code.function/filepath/lineno → nodes diretos.
     Logs com file:line → nodes diretos.
     Spans HTTP cliente/servidor → arestas REQUESTS entre serviços (parte 2) ligam as slices.
   O resolver aplica a prioridade da seção 1.4.
4. Identifica o sintoma: ErrorEvent com maior profundidade na stack, ou span com status=error
   mais profundo na árvore, ou último evento em ordem temporal.
5. Reduz o cone candidato:
     cone = BFS reversa de FLOWS_TO + CALLS a partir do sintoma no grafo,
            interseccionado com a slice de execução (passo 3).
6. Para enriquecer o ranking, consulta o provider sob demanda:
     provider.RecentErrors(nodes_no_cone, since=deploy_anterior)
     provider.QueryMetrics(nodes_no_cone, window=24h)
   (Opcional; se o provider não implementa, o ranking usa só histórico de commits.)
7. Rankeia hipóteses:
   a) Nodes que (i) estão no cone E (ii) mudaram em commits entre deploy anterior e C.
      → forte: "esta função mudou e participou do trace".
   b) Nodes que (i) estão no cone E (ii) têm error_rate alto na janela recente
      (vindo de QueryMetrics).
      → médio: "esta função falha com frequência e participou".
   c) Nodes que (i) estão no cone E (ii) chamam External/Package.
      → fraco: "pode ser falha de dep externa".
   d) Fluxo de dado: FLOWS_TO* do sintoma até parâmetros de entrada / fontes externas
      (READS de env, DB).
      Lista os caminhos; o trace do backend confirma valores.
```

Nenhum passo persiste evento no Neo4j. O trace vive no backend do cliente; o rhaxis o consulta, correlaciona com o grafo, devolve o relatório, descarta.

### 4.4 Exemplo de saída pretendida

```
Trace: 4a7f...   Service: orders-api   Time: 2026-10-02T14:03Z
Deploy: v2.4.1 (sha e3b0c4) · deploy anterior v2.4.0 (ab12cd) há 2h

Sintoma:
  TypeError: Cannot read property 'items' of undefined
  at OrderCalculator.total (src/order/calc.ts:48)

Cone (nodes atingidos pela trace + alcançáveis via FLOWS_TO/CALLS reverso):
  OrderController.create  →  OrderService.enrich  →  OrderCalculator.total ← sintoma
                                                   ↘  Discount.apply
  REQUESTS: users-api GET /users/:id (resposta no span 7, status 200)

Hipóteses rankeadas:

  #1 (confiança alta — mudança recente no cone)
     OrderService.enrich  (body + deps)  @ sha a1b2c3  "ajusta pipeline de enrichment"
       - FLOWS_TO: Order.items é setado condicionalmente agora (if branch novo)
       - no span 5 (duration=0.3ms) função rodou mas não setou items quando usuário é premium
       Caminho: enrich(order) → total(order) → order.items (undefined)
       Suspeito do PR #127

  #2 (confiança média — alta taxa de erro histórica)
     Discount.apply (error_rate 24h = 3.2%)
       Não mudou neste release, mas é fonte conhecida de null em order.

  #3 (confiança baixa — dep externa)
     users-api GET /users/:id respondeu 200 mas sem campo `tier`
       Rhaxis não valida o response; checar contrato (parte 2 com OpenAPI)

Caminhos de dado:
  order.items ← OrderService.enrich.result ← OrderService.enrich (if branch user.tier == 'premium')
             ← OrderController.create.body ← HTTP body (external)

Pode ser (fora do grafo):
  - estado do banco (campo `tier` ausente em um subset de usuários)
  - latência: span users-api levou 450ms, perto do timeout — se timeout, enrich pularia
```

### 4.5 O que falta pra isso virar realidade

| Peça | Status |
|---|---|
| Parser estático + grafo de código | ✅ pronto (Ondas 1–7) |
| `Loc → nodeID` resolver | ✅ pronto (etapa 4 do change-analysis) |
| Histórico/versões com `Commit→CHANGED→Function` | ⏳ `change-analysis-plan.md` etapa 11 |
| `Deployment→DEPLOYS→Commit` | ⏳ `change-analysis-plan.md` 12.6 |
| Interface `TelemetryProvider` + ≥1 adapter (OTEL ou Sentry) | ⏳ parte 1 deste doc |
| Multi-serviço com `REQUESTS` | ⏳ parte 2 deste doc |
| Comando `rhaxis explain -trace <id>` | depende das 4 acima |

Nenhuma das peças é mágica — todas são engenharia direta. A soma delas é que entrega a sensação de "determinismo".

### 4.6 Honestidade sobre determinismo

Determinístico significa: mesmo input → mesmo output, sem heurística opaca.

- **Localização do sintoma:** determinístico, se há stack ou OTEL.
- **Cone de propagação possível:** determinístico (BFS em grafo); mas **superset** do que realmente aconteceu.
- **Cone efetivamente percorrido:** determinístico **se** OTEL instrumentou cada chamada. Senão é inferido a partir dos spans existentes + grafo (semi).
- **Ranking de causa:** heurística com pesos explícitos. Não é determinístico no sentido de "única resposta", mas é **reprodutível** (mesmo trace + mesmo grafo → mesmo ranking).

A ferramenta promete: **reduzir o espaço de causas de "o repositório inteiro" para "N candidatos ordenados"**, com rastro explícito de porque cada um está no ranking. Isso já é um salto gigante em relação a `grep` no log.

### 4.7 Riscos

| Risco | Mitigação |
|---|---|
| Sem OTEL granular, cone efetivo = cone estático inteiro (ruim) | Pedir ao time instrumentar funções críticas; mostrar explicitamente no relatório que a slice está pobre |
| Logs sem `trace_id` | Correlação por janela temporal + serviço + loc (baixa confiança marcada) |
| Backend do cliente fora do ar durante o `explain` | Degradação: devolve só o cone estático + mudanças recentes; avisa que telemetria falhou |
| Deploy fora de banda, commit extraído ≠ commit em prod | `Deployment` + verificação do SHA do build; sem match, warn e segue com HEAD |
| Bug é de infra (DNS, OOM) não de código | Grafo mostra "nenhuma mudança no cone + nenhum erro histórico" — pista de que não é código |
| Falso positivo em hipótese #1 (mudança não causa, só coincide) | Mostrar o caminho exato de FLOWS_TO; humano valida em segundos vs. horas |
| Backend não expõe `code.function` → cone efetivo quase vazio | Degradação: cone vira o cone estático; relatório marca "slice indeterminada" |

---

## Ordem sugerida (visão integrada com change-analysis-plan.md)

| Fase | Entrega | Depende de |
|---|---|---|
| F0 | Fechar M1 do grafo estático (ondas 1–7 já estão em `implementation-status.md`) | — |
| F1 | `change-analysis` etapas 1–10 (git source, cache, diff, impacto, suspeitos) | F0 |
| F2 | `change-analysis` etapa 11 (histórico/versões/autoria) | F1 |
| F3 | Parte 3 deste doc (libs: deduplicação + extração sob demanda) | F0 — pode ser paralelo com F1 |
| F4 | Parte 2 deste doc (manifesto de serviços, `REQUESTS`, `PRODUCES`, `CONSUMES`) | F0 + F3 (para resolver SDKs internos) |
| F5 | Parte 1 deste doc (interface `TelemetryProvider` + 1 adapter: OTEL ou Sentry) | F2 (precisa saber qual commit estava em prod no evento) |
| F6 | `change-analysis` etapa 12 (CI, deploys, incidentes básicos por stacktrace) | F2 |
| F7 | Parte 4 deste doc (`rhaxis explain -trace <id>`) | F2 + F4 + F5 + F6 |
| F8+ | Adapters adicionais (Grafana, Honeycomb, Datadog…) | F5 |

**Primeiro valor incremental:** F3 (libs) sozinha já reduz muito o `External` opaco. F4 sem F5 já responde "quem chama meu endpoint". F7 é a soma — não precisa esperar tudo pra começar a entregar valor.

**Fora de escopo (podem virar produtos separados no futuro):**
- `rhaxis-collector`: receiver OTLP próprio com storage e agregação, para clientes que querem retenção longa fora do backend principal.
- Dashboard próprio de métricas: hoje a visualização fica a cargo do backend do cliente (Grafana, Honeycomb UI, etc.).
- Alerting próprio: idem — rhaxis só recebe webhook do sistema de alerting existente.
