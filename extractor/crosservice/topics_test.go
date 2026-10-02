package crosservice_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor/crosservice"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source/local"
)

// mkFn devolve uma Function cobrindo [startLine..endLine] no fileID dado.
func mkFn(id, fileID string, startLine, endLine int) entity.Function {
	return entity.Function{CodeBase: entity.CodeBase{
		Base: entity.Base{NodeID: id},
		Loc:  entity.Location{FileID: fileID, StartLine: startLine, EndLine: endLine},
	}}
}

func countEdges(g *entity.Graph, from, to string, typ entity.EdgeType) int {
	n := 0
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Type == typ {
			n++
		}
	}
	return n
}

func hasTopic(g *entity.Graph, id string) bool {
	for _, n := range g.Nodes {
		if t, ok := n.(entity.Topic); ok && t.ID() == id {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTopicsProducesFromKafkaSend(t *testing.T) {
	dir := t.TempDir()
	// Linha 1 abre fn, linha 2 publish, linha 3 fecha
	writeFile(t, dir, "a.ts", "function pub() {\n  kafka.send('orders.created', payload);\n}\n")

	app := appNode("svc")
	fileID := app.Key + ":a.ts"
	fn := mkFn("app:svc:a.ts:pub", fileID, 1, 3)
	g := &entity.Graph{Nodes: []entity.Node{app, fn}}

	src := local.New(dir, map[string]string{".ts": "typescript"})
	e := crosservice.TopicsEnricher{Src: src}
	if err := e.Enrich(context.Background(), app, g); err != nil {
		t.Fatal(err)
	}
	tid := "topic:kafka:orders.created"
	if !hasTopic(g, tid) {
		t.Fatalf("topic %s não foi emitido; nodes=%v", tid, g.Nodes)
	}
	if countEdges(g, fn.ID(), tid, entity.ProducesEdge) != 1 {
		t.Errorf("esperado 1 PRODUCES de %s para %s", fn.ID(), tid)
	}
}

func TestTopicsConsumesFromSubscribe(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "b.ts", "function sub() {\n  consumer.subscribe('user.created', handler);\n}\n")

	app := appNode("svc")
	fileID := app.Key + ":b.ts"
	fn := mkFn("app:svc:b.ts:sub", fileID, 1, 3)
	g := &entity.Graph{Nodes: []entity.Node{app, fn}}

	src := local.New(dir, map[string]string{".ts": "typescript"})
	e := crosservice.TopicsEnricher{Src: src}
	if err := e.Enrich(context.Background(), app, g); err != nil {
		t.Fatal(err)
	}
	tid := "topic:kafka:user.created"
	if countEdges(g, fn.ID(), tid, entity.ConsumesEdge) != 1 {
		t.Errorf("esperado 1 CONSUMES de %s para %s", fn.ID(), tid)
	}
}

func TestTopicsObjectStyleSendMessageSQS(t *testing.T) {
	dir := t.TempDir()
	// SQS .sendMessage com QueueUrl → broker=sqs, name="my-queue" (strip da URL)
	body := "function enqueue() {\n" +
		"  sqs.sendMessage({QueueUrl: 'https://sqs.us-east-1.amazonaws.com/123/my-queue', MessageBody: 'x'});\n" +
		"}\n"
	writeFile(t, dir, "c.ts", body)

	app := appNode("svc")
	fileID := app.Key + ":c.ts"
	fn := mkFn("app:svc:c.ts:enq", fileID, 1, 3)
	g := &entity.Graph{Nodes: []entity.Node{app, fn}}

	src := local.New(dir, map[string]string{".ts": "typescript"})
	e := crosservice.TopicsEnricher{Src: src}
	if err := e.Enrich(context.Background(), app, g); err != nil {
		t.Fatal(err)
	}
	tid := "topic:sqs:my-queue"
	if !hasTopic(g, tid) {
		t.Fatalf("topic %s não emitido", tid)
	}
	if countEdges(g, fn.ID(), tid, entity.ProducesEdge) != 1 {
		t.Errorf("esperado 1 PRODUCES para %s", tid)
	}
}

func TestTopicsCallOutsideFunctionIsSkipped(t *testing.T) {
	dir := t.TempDir()
	// publish está na linha 1, mas a Function cobre apenas linhas 3..5
	writeFile(t, dir, "d.ts", "kafka.send('top-level', x);\n\nfunction f() {\n  return 1;\n}\n")

	app := appNode("svc")
	fileID := app.Key + ":d.ts"
	fn := mkFn("app:svc:d.ts:f", fileID, 3, 5)
	g := &entity.Graph{Nodes: []entity.Node{app, fn}}

	src := local.New(dir, map[string]string{".ts": "typescript"})
	e := crosservice.TopicsEnricher{Src: src}
	if err := e.Enrich(context.Background(), app, g); err != nil {
		t.Fatal(err)
	}
	if hasTopic(g, "topic:kafka:top-level") {
		t.Error("topic de call fora de qualquer Function não deveria ser emitido")
	}
}

func TestTopicsSharedAcrossApps(t *testing.T) {
	// Dois apps publicam/consomem o mesmo topic → devem convergir no mesmo node ID.
	dirA := t.TempDir()
	writeFile(t, dirA, "a.ts", "function p() {\n  kafka.send('shared', x);\n}\n")
	appA := appNode("producer-app")
	fnA := mkFn("app:producer-app:a.ts:p", appA.Key+":a.ts", 1, 3)
	gA := &entity.Graph{Nodes: []entity.Node{appA, fnA}}
	srcA := local.New(dirA, map[string]string{".ts": "typescript"})
	if err := (crosservice.TopicsEnricher{Src: srcA}).Enrich(context.Background(), appA, gA); err != nil {
		t.Fatal(err)
	}

	dirB := t.TempDir()
	writeFile(t, dirB, "b.ts", "function c() {\n  consumer.subscribe('shared', h);\n}\n")
	appB := appNode("consumer-app")
	fnB := mkFn("app:consumer-app:b.ts:c", appB.Key+":b.ts", 1, 3)
	gB := &entity.Graph{Nodes: []entity.Node{appB, fnB}}
	srcB := local.New(dirB, map[string]string{".ts": "typescript"})
	if err := (crosservice.TopicsEnricher{Src: srcB}).Enrich(context.Background(), appB, gB); err != nil {
		t.Fatal(err)
	}

	tid := "topic:kafka:shared"
	if !hasTopic(gA, tid) || !hasTopic(gB, tid) {
		t.Fatalf("topic %s deveria estar em ambos os grafos", tid)
	}
	if countEdges(gA, fnA.ID(), tid, entity.ProducesEdge) != 1 {
		t.Error("gA sem PRODUCES para o topic compartilhado")
	}
	if countEdges(gB, fnB.ID(), tid, entity.ConsumesEdge) != 1 {
		t.Error("gB sem CONSUMES para o topic compartilhado")
	}
}

func TestTopicsNoDuplicateWhenTopicAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.ts", "function p() {\n  kafka.send('existing', x);\n}\n")

	app := appNode("svc")
	fileID := app.Key + ":a.ts"
	fn := mkFn("app:svc:a.ts:p", fileID, 1, 3)
	// Topic pré-existente (ex.: outra rodada já o criou)
	preTopic := entity.Topic{Base: entity.Base{NodeID: "topic:kafka:existing"}, Name: "existing", Broker: "kafka"}
	g := &entity.Graph{Nodes: []entity.Node{app, fn, preTopic}}

	src := local.New(dir, map[string]string{".ts": "typescript"})
	e := crosservice.TopicsEnricher{Src: src}
	if err := e.Enrich(context.Background(), app, g); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, nd := range g.Nodes {
		if t, ok := nd.(entity.Topic); ok && t.ID() == "topic:kafka:existing" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("topic duplicado: %d ocorrências", n)
	}
}

func TestTopicsNoSrcIsNoop(t *testing.T) {
	app := appNode("svc")
	g := &entity.Graph{Nodes: []entity.Node{app}}
	e := crosservice.TopicsEnricher{} // Src nil
	if err := e.Enrich(context.Background(), app, g); err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if _, ok := n.(entity.Topic); ok {
			t.Error("sem Src não deveria ter Topic")
		}
	}
}
