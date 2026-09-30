# Fixtures

Dois projetos que exercitam o extrator:

- **fixture-a-nest** — TypeScript estilo NestJS. Cobre interfaces, classes abstratas, herança, `implements`, enums, decorators, tipos de retorno, imports com `paths` do tsconfig.
- **fixture-b-cli** — JavaScript com CommonJS. Cobre `require`/`module.exports`, arrow functions, closures, herança de classes JS, `super`, chamadas a pacotes externos (`node:fs/promises`).

Juntos cobrem todos os tipos que o parser JS/TS atual emite:

- **Nodes**: `Application`, `File`, `Package`, `External`, `Function`, `Parameter`, `Variable`, `Class`, `Interface`, `Field`, `Enum`, `EnumMember`, `Call`.
- **Edges**: `CONTAINS`, `DECLARES`, `HAS_PARAMETER`, `HAS_FIELD`, `HAS_METHOD`, `HAS_MEMBER`, `EXPORTS`, `IMPORTS`, `READS`, `WRITES`, `ARGUMENT`, `CALLS`, `INSTANTIATES`, `HAS_TYPE`, `RETURNS_TYPE`, `EXTENDS`, `IMPLEMENTS`, `INVOKES`.

## Não cobertos (parser ainda não emite)

- Nodes: `Repository`, `Module`, `Endpoint`, `HttpParam`, e os de statement (`Assignment`, `Return`, `Throw`, `If`, `Loop`, `Switch`, `Case`, `Try`, `Catch`).
- Edges: `EXPOSES`, `HANDLED_BY`, `HAS_PARAM`, `BINDS`, `REQUESTS`, `FLOWS_TO`, `NEXT`.

Existem no `entity` como parte do modelo, mas ainda não são gerados. O teste de cobertura em `extractor/extractor_integration_test.go` valida apenas o conjunto que o parser efetivamente produz.
