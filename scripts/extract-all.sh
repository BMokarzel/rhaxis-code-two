#!/usr/bin/env bash
# Sobe o Neo4j via compose, roda a extração das duas fixtures e imprime contagens no banco.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [ ! -f .env ]; then
  echo ".env não encontrado. Copiando de .env.example." >&2
  cp .env.example .env
fi

set -a
# shellcheck disable=SC1091
. ./.env
set +a

echo "== docker compose up -d neo4j"
docker compose up -d neo4j

echo "== aguardando healthcheck do Neo4j"
for i in $(seq 1 60); do
  status=$(docker inspect -f '{{.State.Health.Status}}' rhaxis-neo4j 2>/dev/null || echo "starting")
  if [ "$status" = "healthy" ]; then
    echo "Neo4j saudável."
    break
  fi
  sleep 2
done
if [ "$status" != "healthy" ]; then
  echo "Neo4j não subiu a tempo (status=$status)" >&2
  exit 1
fi

extract_fixture() {
  local path="$1"
  local key="$2"
  echo "== extraindo $key ($path)"
  NEO4J_URI="$NEO4J_URI" \
    NEO4J_USER="$NEO4J_USER" \
    NEO4J_PASSWORD="$NEO4J_PASSWORD" \
    go run ./extractor/cmd -path "$path" -key "$key"
}

extract_fixture "./testdata/fixture-a-nest" "fixture-a"
extract_fixture "./testdata/fixture-b-cli" "fixture-b"

echo "== sanity queries"
cypher() {
  docker exec -i rhaxis-neo4j cypher-shell -u "$NEO4J_USER" -p "$NEO4J_PASSWORD" --format plain "$1"
}

echo "-- Applications:"
cypher "MATCH (a:Application) RETURN a.id AS id, a.name AS name ORDER BY a.id;"

echo "-- Node counts per label (por aplicação):"
for key in fixture-a fixture-b; do
  echo "  app=$key"
  cypher "MATCH (n) WHERE n.id STARTS WITH '${key}:' RETURN labels(n)[0] AS label, count(n) AS n ORDER BY label;"
done

echo "-- Edge counts per type (por aplicação):"
for key in fixture-a fixture-b; do
  echo "  app=$key"
  cypher "MATCH (a)-[r]->(b) WHERE a.id STARTS WITH '${key}:' RETURN type(r) AS type, count(r) AS n ORDER BY type;"
done

echo "== pronto. Abra http://localhost:${NEO4J_HTTP_PORT} (user=$NEO4J_USER) para inspecionar."
