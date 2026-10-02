package crosservice

import (
	"bytes"
	"context"
	"regexp"
	"sort"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor/source"
)

// TopicsEnricher varre o conteúdo dos arquivos procurando chamadas a clientes
// de mensageria e emite nodes `Topic` + edges PRODUCES/CONSUMES ligando a
// Function que contém a linha ao Topic. IDs de Topic são globais
// (`topic:<broker>:<name>`), portanto apps diferentes convergem no mesmo node
// — é como o cross-link acontece sem edge direta entre apps.
//
// Padrões detectados:
//
//	PRODUCES: <obj>.publish('T', ...), .produce(...), .send('T', ...),
//	          .sendToQueue('Q', ...), .sendMessage({QueueUrl:'...X'})
//	CONSUMES: <obj>.subscribe('T', ...), .consume('T', ...),
//	          .receiveMessage({QueueUrl:'...X'}), .psubscribe('T', ...)
//
// Brokers inferidos pelo prefixo do callee (kafka*, producer, consumer, rabbit*,
// channel, sqs*, sns*, redis*, pubsub*). Prefixo não reconhecido → broker "unknown".
type TopicsEnricher struct {
	Src source.Provider
}

func (e TopicsEnricher) Enrich(ctx context.Context, app entity.Application, g *entity.Graph) error {
	if e.Src == nil {
		return nil
	}
	fnsByFile := collectFunctionsByFile(g)
	if len(fnsByFile) == 0 {
		return nil
	}
	topicsSeen := map[string]bool{}
	for _, n := range g.Nodes {
		if _, ok := n.(entity.Topic); ok {
			topicsSeen[n.ID()] = true
		}
	}
	return e.Src.Walk(ctx, func(f source.File) error {
		fileID := app.Key + ":" + f.Path
		fns, ok := fnsByFile[fileID]
		if !ok {
			return nil
		}
		data, err := e.Src.ReadFile(ctx, f.Path)
		if err != nil {
			return nil
		}
		hits := scanTopicCalls(data)
		for _, h := range hits {
			fn := innermostFn(fns, h.Line)
			if fn == nil {
				continue
			}
			tid := "topic:" + h.Broker + ":" + h.Name
			if !topicsSeen[tid] {
				g.Nodes = append(g.Nodes, entity.Topic{
					Base:   entity.Base{NodeID: tid},
					Name:   h.Name,
					Broker: h.Broker,
				})
				topicsSeen[tid] = true
			}
			edgeType := entity.ProducesEdge
			if h.Consumes {
				edgeType = entity.ConsumesEdge
			}
			g.Edges = append(g.Edges, entity.Edge{
				Type:       edgeType,
				From:       fn.ID,
				To:         tid,
				Resolution: entity.ResolutionInferred,
			})
		}
		return nil
	})
}

type fnDecl struct {
	ID      string
	FileID  string
	StartLn int
	EndLn   int
	Span    int
}

func collectFunctionsByFile(g *entity.Graph) map[string][]fnDecl {
	out := map[string][]fnDecl{}
	for _, n := range g.Nodes {
		fn, ok := n.(entity.Function)
		if !ok {
			continue
		}
		loc := fn.Location()
		if loc.FileID == "" || loc.StartLine <= 0 || loc.EndLine < loc.StartLine {
			continue
		}
		out[loc.FileID] = append(out[loc.FileID], fnDecl{
			ID:      fn.ID(),
			FileID:  loc.FileID,
			StartLn: loc.StartLine,
			EndLn:   loc.EndLine,
			Span:    loc.EndLine - loc.StartLine + 1,
		})
	}
	// span ascendente → innermost é o primeiro match
	for k := range out {
		decls := out[k]
		sort.Slice(decls, func(i, j int) bool { return decls[i].Span < decls[j].Span })
		out[k] = decls
	}
	return out
}

func innermostFn(fns []fnDecl, line int) *fnDecl {
	for i := range fns {
		if fns[i].StartLn <= line && line <= fns[i].EndLn {
			return &fns[i]
		}
	}
	return nil
}

type topicHit struct {
	Broker   string
	Name     string
	Line     int
	Consumes bool // false = produce
}

// positionalRE casa chamadas no formato: <callee>.<method>('name', ...) ou
// variantes com aspas duplas / backtick. Argumentos nomeados ({topic: 'X'})
// são tratados por objectRE separadamente.
var positionalRE = regexp.MustCompile(
	`(?i)\b([a-z_][a-z0-9_]*)\s*\.\s*(publish|produce|send|sendToQueue|sendMessage|consume|subscribe|receiveMessage|psubscribe)\s*\(\s*['"` + "`" + `]([^'"` + "`" + `\s,)]+)['"` + "`" + `]`,
)

// objectRE casa <callee>.<method>({topic|QueueUrl|TopicArn: 'name', ...}).
// Pega o primeiro valor literal após `topic:`/`QueueUrl:`/`TopicArn:`.
var objectRE = regexp.MustCompile(
	`(?i)\b([a-z_][a-z0-9_]*)\s*\.\s*(publish|produce|send|sendToQueue|sendMessage|consume|subscribe|receiveMessage|psubscribe)\s*\(\s*\{[^}]*?\b(?:topic|QueueUrl|TopicArn)\s*:\s*['"` + "`" + `]([^'"` + "`" + `]+)['"` + "`" + `]`,
)

// scanTopicCalls faz regex sobre o buffer e devolve hits com linha calculada
// pelo contador de `\n` até o offset do match. Dedup por (broker, name, action, line).
func scanTopicCalls(data []byte) []topicHit {
	seen := map[string]bool{}
	var out []topicHit
	for _, re := range []*regexp.Regexp{positionalRE, objectRE} {
		for _, m := range re.FindAllSubmatchIndex(data, -1) {
			if len(m) < 8 {
				continue
			}
			callee := string(data[m[2]:m[3]])
			method := string(data[m[4]:m[5]])
			rawName := string(data[m[6]:m[7]])
			broker := brokerFromCallee(callee)
			consumes := isConsumeMethod(method)
			name := normalizeTopicName(rawName, broker)
			if name == "" {
				continue
			}
			line := 1 + bytes.Count(data[:m[0]], []byte("\n"))
			key := broker + "|" + name + "|" + boolStr(consumes) + "|" + itoa(line)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, topicHit{Broker: broker, Name: name, Line: line, Consumes: consumes})
		}
	}
	return out
}

func brokerFromCallee(c string) string {
	cl := bytes.ToLower([]byte(c))
	switch {
	case bytes.HasPrefix(cl, []byte("kafka")), bytes.Equal(cl, []byte("producer")), bytes.Equal(cl, []byte("consumer")):
		return "kafka"
	case bytes.HasPrefix(cl, []byte("rabbit")), bytes.Equal(cl, []byte("channel")):
		return "rabbitmq"
	case bytes.HasPrefix(cl, []byte("sqs")):
		return "sqs"
	case bytes.HasPrefix(cl, []byte("sns")):
		return "sns"
	case bytes.HasPrefix(cl, []byte("redis")):
		return "redis"
	case bytes.HasPrefix(cl, []byte("pubsub")):
		return "pubsub"
	}
	return "unknown"
}

func isConsumeMethod(m string) bool {
	switch m {
	case "consume", "subscribe", "receiveMessage", "psubscribe":
		return true
	}
	return false
}

// normalizeTopicName extrai a parte útil de QueueUrls do SQS
// (`https://sqs.us-east-1.amazonaws.com/123/my-queue` → `my-queue`) e ARNs
// do SNS (`arn:aws:sns:us-east-1:123:my-topic` → `my-topic`). Para outros
// brokers devolve o nome como veio.
func normalizeTopicName(raw, broker string) string {
	switch broker {
	case "sqs":
		if i := lastSlash(raw); i >= 0 && i < len(raw)-1 {
			return raw[i+1:]
		}
	case "sns":
		if i := lastColon(raw); i >= 0 && i < len(raw)-1 {
			return raw[i+1:]
		}
	}
	return raw
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

func lastColon(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}

func boolStr(b bool) string {
	if b {
		return "c"
	}
	return "p"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	buf := [20]byte{}
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}
