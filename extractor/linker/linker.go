// Package linker transforma os FileGraphs de uma aplicação em um grafo resolvido:
// cada PendingRef vira uma edge para a declaração correta, atravessando arquivos via import/export.
//
// A parte genérica (escopos, exports, membros de classe, tipos) fica aqui. O que é específico de
// linguagem, como transformar o texto de um import em um arquivo, fica atrás de ModuleResolver.
package linker

import (
	"context"
	"fmt"
	"strings"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/extractor/ir"
)

// Target é o destino de um import: um arquivo da aplicação ou um pacote externo
type Target struct {
	FilePath string
	Package  string
}

type ModuleResolver interface {
	Resolve(fromPath, spec string) (Target, bool)
}

type Input struct {
	App      entity.Application
	Files    []*ir.FileGraph
	ReadFile func(ctx context.Context, path string) ([]byte, error)
}

// ResolverFactory cria o resolver de módulos de uma linguagem para uma execução do linker
type ResolverFactory func(ctx context.Context, in Input) (ModuleResolver, error)

type Linker struct {
	resolvers map[string]ResolverFactory
}

// New recebe as factories de resolver indexadas por linguagem (source.File.Language)
func New(resolvers map[string]ResolverFactory) *Linker {
	return &Linker{resolvers: resolvers}
}

func (l *Linker) Link(ctx context.Context, in Input) (*entity.Graph, error) {
	r, err := newRun(ctx, l, in)
	if err != nil {
		return nil, err
	}
	return r.link(), nil
}

type fileCtx struct {
	fg       *ir.FileGraph
	resolver ModuleResolver
	imports  map[string]ir.Import
	exports  map[string][]ir.Export
	starExp  []ir.Export
}

// symbol é o resultado de resolver um nome; exatamente um dos campos de destino é preenchido
type symbol struct {
	nodeID   string
	module   *fileCtx
	pkg      string
	external string
	// member é o nome importado de um pacote externo: import { readFile } from 'fs' → "readFile"
	member string
	res    entity.Resolution
}

func (s symbol) ok() bool {
	return s.nodeID != "" || s.module != nil || s.pkg != "" || s.external != ""
}

type run struct {
	in      Input
	graph   *entity.Graph
	nodes   map[string]entity.Node
	scopes  map[string]*ir.Scope
	files   map[string]*fileCtx // por path
	byScope map[string]*fileCtx // escopo de topo → arquivo
	members map[string]map[string]string
	extends map[string][]string
	typeOf  map[string]symbol
	created map[string]bool
	edgeSet map[string]bool
}

func newRun(ctx context.Context, l *Linker, in Input) (*run, error) {
	r := &run{
		in:      in,
		graph:   &entity.Graph{},
		nodes:   map[string]entity.Node{},
		scopes:  map[string]*ir.Scope{},
		files:   map[string]*fileCtx{},
		byScope: map[string]*fileCtx{},
		members: map[string]map[string]string{},
		extends: map[string][]string{},
		typeOf:  map[string]symbol{},
		created: map[string]bool{},
		edgeSet: map[string]bool{},
	}
	resolvers := map[string]ModuleResolver{}
	for _, fg := range in.Files {
		lang := fg.File.Language
		res, ok := resolvers[lang]
		if !ok {
			factory, found := l.resolvers[lang]
			if !found {
				return nil, fmt.Errorf("nenhum resolver de módulos para a linguagem %q", lang)
			}
			var err error
			if res, err = factory(ctx, in); err != nil {
				return nil, fmt.Errorf("criando resolver de %s: %w", lang, err)
			}
			resolvers[lang] = res
		}
		fc := &fileCtx{fg: fg, resolver: res, imports: map[string]ir.Import{}, exports: map[string][]ir.Export{}}
		for _, imp := range fg.Imports {
			if imp.Local != "" {
				fc.imports[imp.Local] = imp
			}
		}
		for _, e := range fg.Exports {
			if e.Kind == ir.ExportReexportAll {
				fc.starExp = append(fc.starExp, e)
			} else {
				fc.exports[e.Name] = append(fc.exports[e.Name], e)
			}
		}
		r.files[fg.File.Path] = fc
		r.byScope[fg.FileScopeID] = fc
		for i := range fg.Scopes {
			r.scopes[fg.Scopes[i].ID] = &fg.Scopes[i]
		}
		for _, n := range fg.Nodes {
			r.nodes[n.ID()] = n
		}
	}
	return r, nil
}

func (r *run) link() *entity.Graph {
	r.addNode(r.in.App)
	for _, fg := range r.in.Files {
		r.addNode(fg.File)
		r.addEdge(entity.Edge{Type: entity.ContainsEdge, From: r.in.App.ID(), To: fg.File.ID()})
		for _, n := range fg.Nodes {
			r.addNode(n)
		}
		for _, e := range fg.Edges {
			r.graph.Edges = append(r.graph.Edges, e)
			switch e.Type {
			case entity.HasFieldEdge, entity.HasMethodEdge, entity.HasMemberEdge:
				if name := nameOf(r.nodes[e.To]); name != "" {
					if r.members[e.From] == nil {
						r.members[e.From] = map[string]string{}
					}
					r.members[e.From][name] = e.To
				}
			}
		}
	}

	// tipos primeiro: a resolução de membros (this.repo.find) depende deles
	var rest []ir.PendingRef
	for _, fg := range r.in.Files {
		for _, ref := range fg.Refs {
			switch ref.Edge {
			case entity.ExtendsEdge, entity.ImplementsEdge:
				r.linkType(ref)
			default:
				rest = append(rest, ref)
			}
		}
	}
	var others []ir.PendingRef
	for _, ref := range rest {
		switch ref.Edge {
		case entity.HasTypeEdge, entity.ReturnsTypeEdge, entity.HasTypeArgEdge:
			r.linkType(ref)
		default:
			others = append(others, ref)
		}
	}
	for _, ref := range others {
		r.linkUse(ref)
	}

	for _, fg := range r.in.Files {
		r.linkImports(r.files[fg.File.Path])
	}
	r.deriveInvokes()
	r.deriveFlowsTo()
	return r.graph
}

func (r *run) linkType(ref ir.PendingRef) {
	sym, rest, _ := r.resolvePath(ref.ScopeID, ref.Name, ref.Path)
	if len(rest) > 0 {
		return
	}
	if sym.nodeID == "" {
		// Tipos externos (Map, Array, Date, etc.) não geram HAS_TYPE, mas ainda são
		// registrados em typeOf para que chamadas do tipo `this.field.method()` resolvam
		// para uma aresta CALLS Call → External.
		if ref.Edge == entity.HasTypeEdge && sym.external != "" {
			if _, exists := r.typeOf[ref.From]; !exists {
				r.typeOf[ref.From] = symbol{external: sym.external, res: entity.ResolutionNameOnly}
			}
		}
		return
	}
	switch r.nodes[sym.nodeID].Type() {
	case entity.ClassNode, entity.InterfaceNode, entity.EnumNode:
	default:
		return
	}
	res := sym.res
	if ref.Inferred {
		res = entity.ResolutionInferred
	}
	r.addEdge(entity.Edge{Type: ref.Edge, From: ref.From, To: sym.nodeID, Index: ref.Index, Resolution: res})
	switch ref.Edge {
	case entity.ExtendsEdge, entity.ImplementsEdge:
		r.extends[ref.From] = append(r.extends[ref.From], sym.nodeID)
	case entity.HasTypeEdge, entity.ReturnsTypeEdge:
		// HAS_TYPE_ARG não registra typeOf: o tipo "efetivo" do portador continua sendo o container
		// (ex.: Map<string, User> → typeOf é Map, não User), para que `.get()` resolva no container.
		if _, exists := r.typeOf[ref.From]; !exists {
			r.typeOf[ref.From] = symbol{nodeID: sym.nodeID, res: res}
		}
	}
}

func (r *run) linkUse(ref ir.PendingRef) {
	sym, rest, chain := r.resolvePath(ref.ScopeID, ref.Name, ref.Path)
	loc := ref.Loc
	to := r.materialize(sym)
	member := strings.Join(append(nonEmpty(sym.member), rest...), ".")
	// WRITES vindas de destructuring carregam o key/index de origem no PendingRef.Member;
	// como o Name já é o próprio identificador declarado (não há path), o membro vindo da
	// resolução fica vazio e podemos usar o do ref diretamente.
	if ref.Member != "" && member == "" {
		member = ref.Member
	}
	edge := entity.Edge{
		Type:       ref.Edge,
		From:       ref.From,
		To:         to,
		Index:      ref.Index,
		Loc:        &loc,
		Member:     member,
		Operator:   ref.Operator,
		Resolution: sym.res,
	}
	if ref.Edge == entity.CallsEdge && sym.nodeID != "" {
		// alvo de chamada que não é função (ex.: this.repo.save() com repo sem tipo conhecido):
		// registra a dependência do valor em vez de uma CALLS falsa
		if t := r.nodes[sym.nodeID].Type(); t != entity.FunctionNode && t != entity.ClassNode || len(rest) > 0 {
			edge.Type = entity.ReadsEdge
		}
	}
	r.addEdge(edge)

	// os valores intermediários da cadeia também são lidos: f(user.email) lê a variável user
	for _, id := range chain {
		if id == sym.nodeID {
			continue
		}
		switch r.nodes[id].Type() {
		case entity.VariableNode, entity.ParameterNode, entity.FieldNode:
			r.addEdge(entity.Edge{Type: entity.ReadsEdge, From: ref.From, To: id, Loc: &loc, Resolution: entity.ResolutionExact})
		}
	}
}

func (r *run) linkImports(fc *fileCtx) {
	for _, imp := range fc.fg.Imports {
		t, ok := fc.resolver.Resolve(fc.fg.File.Path, imp.Source)
		if !ok {
			continue
		}
		var to string
		if t.Package != "" {
			to = r.materialize(symbol{pkg: t.Package})
		} else if target, found := r.files[t.FilePath]; found {
			to = target.fg.File.ID()
		} else {
			continue
		}
		r.addEdge(entity.Edge{Type: entity.ImportsEdge, From: fc.fg.File.ID(), To: to, Resolution: entity.ResolutionExact})
		// BINDS por símbolo: rastreia quais nomes específicos atravessam a fronteira.
		// IMPORTS é a aresta grossa por arquivo/pacote; BINDS é fina por símbolo.
		switch imp.Kind {
		case ir.ImportNamed, ir.ImportDefault:
			sym := r.resolveImport(fc, imp)
			if id := r.materializeBind(sym); id != "" {
				edge := entity.Edge{Type: entity.BindsEdge, From: fc.fg.File.ID(), To: id, Resolution: sym.res}
				if imp.Local != "" && imp.Imported != "" && imp.Local != imp.Imported {
					edge.Member = imp.Local // alias local (import { A as C })
				}
				r.addEdge(edge)
			}
		}
	}
	// Barrel re-exports (`export * from`, `export { X } from`): a fonte é uma dependência
	// (IMPORTS) e cada símbolo re-exportado precisa da aresta EXPORTS do barrel para o node.
	for _, e := range fc.fg.Exports {
		if e.Source == "" {
			continue
		}
		t, ok := fc.resolver.Resolve(fc.fg.File.Path, e.Source)
		if !ok {
			continue
		}
		var to string
		if t.Package != "" {
			to = r.materialize(symbol{pkg: t.Package})
		} else if target, found := r.files[t.FilePath]; found {
			to = target.fg.File.ID()
		}
		if to != "" {
			r.addEdge(entity.Edge{Type: entity.ImportsEdge, From: fc.fg.File.ID(), To: to, Resolution: entity.ResolutionExact})
		}
		switch e.Kind {
		case ir.ExportReexport:
			sym := r.followReexport(fc, e, e.Local, map[string]bool{})
			if id := r.materializeExport(sym); id != "" {
				r.addEdge(entity.Edge{Type: entity.ExportsEdge, From: fc.fg.File.ID(), To: id, Resolution: sym.res})
			}
		case ir.ExportReexportAll:
			if t.Package != "" {
				continue // pacote externo: sem lista de exportados
			}
			target, found := r.files[t.FilePath]
			if !found {
				continue
			}
			for name := range target.exports {
				if name == "default" {
					continue
				}
				sym := r.resolveExport(target, name, map[string]bool{})
				if id := r.materializeExport(sym); id != "" {
					r.addEdge(entity.Edge{Type: entity.ExportsEdge, From: fc.fg.File.ID(), To: id, Resolution: sym.res})
				}
			}
		}
	}
}

// materializeExport devolve o node ID de destino de um símbolo para uma aresta EXPORTS.
// Diferente de materialize() genérico, evita criar External para exports não resolvidos.
func (r *run) materializeExport(sym symbol) string {
	switch {
	case sym.nodeID != "":
		return sym.nodeID
	case sym.module != nil:
		return sym.module.fg.File.ID()
	case sym.pkg != "":
		return r.materialize(sym)
	}
	return ""
}

// materializeBind resolve o alvo de uma aresta BINDS.
// Para `import { readFile } from 'node:fs/promises'`, cria (uma vez) um External `readFile`
// e retorna seu ID; o Package `node:fs` continua sendo o alvo da IMPORTS.
func (r *run) materializeBind(sym symbol) string {
	switch {
	case sym.nodeID != "":
		return sym.nodeID
	case sym.module != nil:
		return sym.module.fg.File.ID()
	case sym.pkg != "":
		if sym.member == "" {
			return r.materialize(symbol{pkg: sym.pkg})
		}
		// External nomeado, sob o package: id = pkg:<pkg>/<member>
		id := "pkg:" + sym.pkg + "/" + sym.member
		if !r.created[id] {
			r.created[id] = true
			r.addNode(entity.External{Base: entity.Base{NodeID: id}, Name: sym.member})
		}
		return id
	}
	return ""
}

// deriveFlowsTo emite FLOWS_TO a partir das edges intra-procedurais já resolvidas:
//
//  1. Assignment: cada `Assignment -READS-> R` e `Assignment -WRITES-> W` produz `R -FLOWS_TO-> W`.
//     Cobre `x = y`, `x = foo()` (R = Call → W = Var), `this.f = y`, destructuring parcial.
//  2. Return: cada `Return -READS-> R` produz `R -FLOWS_TO-> Return`; a Return também
//     emite `Return -FLOWS_TO-> Function(owner)` para o inter-procedural chainar via
//     CALLS + Assignment do caller.
//  3. Throw: análogo a Return (fluxo até o node Throw; Throw → Function não é emitido
//     porque semanticamente o valor sai pela borda de exceção, não pelo retorno).
//  4. Argumento → Parâmetro: `Call -ARGUMENT{i}-> X` + `Call -CALLS-> F` + `F -HAS_PARAMETER-> P{Index=i}`
//     produz `X -FLOWS_TO-> P`. Só emite quando o alvo da CALLS é um Function node.
//
// FLOWS_TO é sempre emitido sem Loc, então duplicatas são absorvidas por addEdge.
// Não emite auto-loops (R == W).
func (r *run) deriveFlowsTo() {
	type writeTarget struct {
		id     string
		member string
	}
	writesByAssign := map[string][]writeTarget{}
	readsByAssign := map[string][]string{}
	readsByReturn := map[string][]string{}
	readsByThrow := map[string][]string{}
	argsByCall := map[string]map[int]string{}
	callsTarget := map[string]string{}
	paramsByFunc := map[string]map[int]string{}

	for _, e := range r.graph.Edges {
		from, okF := r.nodes[e.From]
		if !okF {
			continue
		}
		switch e.Type {
		case entity.WritesEdge:
			if from.Type() == entity.AssignmentNode && e.To != "" {
				writesByAssign[e.From] = append(writesByAssign[e.From], writeTarget{id: e.To, member: e.Member})
			}
		case entity.ReadsEdge:
			if e.To == "" {
				continue
			}
			switch from.Type() {
			case entity.AssignmentNode:
				readsByAssign[e.From] = append(readsByAssign[e.From], e.To)
			case entity.ReturnNode:
				readsByReturn[e.From] = append(readsByReturn[e.From], e.To)
			case entity.ThrowNode:
				readsByThrow[e.From] = append(readsByThrow[e.From], e.To)
			}
		case entity.ArgumentEdge:
			if from.Type() == entity.CallNode && e.Index != nil && e.To != "" {
				if argsByCall[e.From] == nil {
					argsByCall[e.From] = map[int]string{}
				}
				argsByCall[e.From][*e.Index] = e.To
			}
		case entity.CallsEdge:
			if from.Type() == entity.CallNode {
				if to, ok := r.nodes[e.To]; ok && to.Type() == entity.FunctionNode {
					callsTarget[e.From] = e.To
				}
			}
		case entity.HasParameterEdge:
			if p, ok := r.nodes[e.To].(entity.Parameter); ok {
				if paramsByFunc[e.From] == nil {
					paramsByFunc[e.From] = map[int]string{}
				}
				paramsByFunc[e.From][p.Index] = e.To
			}
		}
	}

	// Regra 1: Assignment reads → Assignment writes. Em destructuring (`const {a} = obj`),
	// o WRITES Assignment→a carrega Member="a"; propagamos para o FLOWS_TO para que o grafo
	// expresse "obj flui para a especificamente pelo membro a" em vez de só "obj flui para a".
	for aid, writes := range writesByAssign {
		reads := readsByAssign[aid]
		for _, w := range writes {
			for _, rr := range reads {
				if rr == w.id {
					continue
				}
				r.addEdge(entity.Edge{Type: entity.FlowsToEdge, From: rr, To: w.id, Member: w.member, Resolution: entity.ResolutionExact})
			}
		}
	}

	// Regra 2: Return reads → Return → Function(owner)
	for rid, reads := range readsByReturn {
		for _, rr := range reads {
			if rr == rid {
				continue
			}
			r.addEdge(entity.Edge{Type: entity.FlowsToEdge, From: rr, To: rid, Resolution: entity.ResolutionExact})
		}
		if ret, ok := r.nodes[rid].(entity.Code); ok {
			if owner := ret.Owner(); owner != "" {
				if ownerNode, okO := r.nodes[owner]; okO && ownerNode.Type() == entity.FunctionNode {
					r.addEdge(entity.Edge{Type: entity.FlowsToEdge, From: rid, To: owner, Resolution: entity.ResolutionExact})
				}
			}
		}
	}

	// Regra 3: Throw reads → Throw
	for tid, reads := range readsByThrow {
		for _, rr := range reads {
			if rr == tid {
				continue
			}
			r.addEdge(entity.Edge{Type: entity.FlowsToEdge, From: rr, To: tid, Resolution: entity.ResolutionExact})
		}
	}

	// Regra 4: Argumento → Parâmetro
	for cid, args := range argsByCall {
		fnID, ok := callsTarget[cid]
		if !ok {
			continue
		}
		params := paramsByFunc[fnID]
		if params == nil {
			continue
		}
		for idx, argID := range args {
			if pid, ok := params[idx]; ok && pid != argID {
				r.addEdge(entity.Edge{Type: entity.FlowsToEdge, From: argID, To: pid, Resolution: entity.ResolutionExact})
			}
		}
	}
}

// deriveInvokes resume CALLS em Function → Function. Também cobre callbacks passados por
// posição a higher-order functions (Promise `.then(fn)`, `.catch(fn)`, `arr.map(fn)`,
// `addEventListener('x', fn)`, etc.) — qualquer `Call -ARGUMENT-> Function` emite
// `owner -INVOKES-> arg` com `Resolution=Inferred`, já que o HOF pode nunca chamar o
// callback (ex.: `register(h)` que só armazena). Preferimos aceitar o falso-positivo em
// troca de ver no grafo o grafo de callbacks real da maioria dos usos.
func (r *run) deriveInvokes() {
	for _, e := range r.graph.Edges {
		switch e.Type {
		case entity.CallsEdge:
			target, ok := r.nodes[e.To]
			if !ok || target.Type() != entity.FunctionNode {
				continue
			}
			call, ok := r.nodes[e.From].(entity.Code)
			if !ok {
				continue
			}
			owner, ok := r.nodes[call.Owner()]
			if !ok || owner.Type() != entity.FunctionNode {
				continue
			}
			r.addEdge(entity.Edge{Type: entity.InvokesEdge, From: owner.ID(), To: target.ID(), Resolution: e.Resolution})
		case entity.ArgumentEdge:
			target, ok := r.nodes[e.To]
			if !ok || target.Type() != entity.FunctionNode {
				continue
			}
			call, ok := r.nodes[e.From].(entity.Code)
			if !ok {
				continue
			}
			owner, ok := r.nodes[call.Owner()]
			if !ok || owner.Type() != entity.FunctionNode {
				continue
			}
			res := weaker(e.Resolution, entity.ResolutionInferred)
			r.addEdge(entity.Edge{Type: entity.InvokesEdge, From: owner.ID(), To: target.ID(), Resolution: res})
		}
	}
}

// resolvePath resolve a raiz pelo escopo e depois cada membro. Retorna o símbolo mais profundo
// alcançado, os membros que não resolveram e os nodes percorridos.
func (r *run) resolvePath(scopeID, name string, path []string) (symbol, []string, []string) {
	sym := r.lookup(scopeID, name)
	var chain []string
	for i, seg := range path {
		if sym.nodeID != "" {
			chain = append(chain, sym.nodeID)
		}
		next, ok := r.member(sym, seg)
		if !ok {
			return sym, path[i:], chain
		}
		sym = next
	}
	return sym, nil, chain
}

func (r *run) lookup(scopeID, name string) symbol {
	// super se resolve para o parent da classe corrente (via cadeia de extends).
	// O parent é aquele em que a busca de membro (super.x) e a construção (super())
	// vão continuar.
	if name == "super" {
		thisSym := r.lookup(scopeID, "this")
		if thisSym.nodeID != "" {
			if parents := r.extends[thisSym.nodeID]; len(parents) > 0 {
				return symbol{nodeID: parents[0], res: entity.ResolutionExact}
			}
		}
		return symbol{}
	}
	for id := scopeID; id != ""; {
		s, ok := r.scopes[id]
		if !ok {
			break
		}
		if nodeID, found := s.Names[name]; found {
			return symbol{nodeID: nodeID, res: entity.ResolutionExact}
		}
		if s.ParentID == "" {
			if fc, isFile := r.byScope[s.ID]; isFile {
				if imp, imported := fc.imports[name]; imported {
					if sym := r.resolveImport(fc, imp); sym.ok() {
						return sym
					}
				}
			}
		}
		id = s.ParentID
	}
	return symbol{external: name, res: entity.ResolutionNameOnly}
}

func (r *run) resolveImport(fc *fileCtx, imp ir.Import) symbol {
	t, ok := fc.resolver.Resolve(fc.fg.File.Path, imp.Source)
	if !ok {
		return symbol{}
	}
	if t.Package != "" {
		sym := symbol{pkg: t.Package, res: entity.ResolutionExact}
		if imp.Kind == ir.ImportNamed {
			sym.member = imp.Imported
		}
		return sym
	}
	target, ok := r.files[t.FilePath]
	if !ok {
		return symbol{}
	}
	switch imp.Kind {
	case ir.ImportNamespace:
		return symbol{module: target, res: entity.ResolutionExact}
	case ir.ImportDefault:
		return r.resolveExport(target, "default", map[string]bool{})
	default:
		return r.resolveExport(target, imp.Imported, map[string]bool{})
	}
}

func (r *run) resolveExport(fc *fileCtx, name string, visited map[string]bool) symbol {
	key := fc.fg.File.Path + "#" + name
	if visited[key] {
		return symbol{}
	}
	visited[key] = true

	for _, e := range fc.exports[name] {
		switch e.Kind {
		case ir.ExportLocal:
			if e.NodeID != "" {
				return symbol{nodeID: e.NodeID, res: entity.ResolutionExact}
			}
			if sym := r.lookup(fc.fg.FileScopeID, e.Local); sym.external == "" {
				return sym
			}
		case ir.ExportReexport:
			if sym := r.followReexport(fc, e, e.Local, visited); sym.ok() {
				return sym
			}
		}
	}
	for _, e := range fc.starExp {
		if name == "default" {
			break
		}
		if sym := r.followReexport(fc, e, name, visited); sym.ok() {
			return sym
		}
	}
	// arquivo sem exports ESM (CommonJS): casa pelo nome de topo
	if len(fc.fg.Exports) == 0 {
		if name == "default" {
			return symbol{module: fc, res: entity.ResolutionInferred}
		}
		if id, ok := r.scopes[fc.fg.FileScopeID].Names[name]; ok {
			return symbol{nodeID: id, res: entity.ResolutionInferred}
		}
	}
	return symbol{}
}

func (r *run) followReexport(fc *fileCtx, e ir.Export, name string, visited map[string]bool) symbol {
	t, ok := fc.resolver.Resolve(fc.fg.File.Path, e.Source)
	if !ok {
		return symbol{}
	}
	if t.Package != "" {
		sym := symbol{pkg: t.Package, res: entity.ResolutionExact}
		if name != "*" {
			sym.member = name
		}
		return sym
	}
	target, ok := r.files[t.FilePath]
	if !ok {
		return symbol{}
	}
	if name == "*" {
		return symbol{module: target, res: entity.ResolutionExact}
	}
	return r.resolveExport(target, name, visited)
}

// member resolve `sym.seg`
func (r *run) member(sym symbol, seg string) (symbol, bool) {
	if sym.module != nil {
		next := r.resolveExport(sym.module, seg, map[string]bool{})
		return next, next.ok()
	}
	if sym.nodeID == "" {
		return symbol{}, false
	}
	owner := sym.nodeID
	res := sym.res
	switch r.nodes[owner].Type() {
	case entity.ClassNode, entity.InterfaceNode, entity.EnumNode:
	default:
		t, ok := r.typeOf[owner]
		if !ok {
			return symbol{}, false
		}
		// Valor com tipo externo (Map, Array, ...): carrega o membro pendente para
		// que a chamada saia como CALLS Call → External com Member = "<method>".
		if t.external != "" {
			return symbol{external: t.external, member: seg, res: weaker(res, t.res)}, true
		}
		owner = t.nodeID
		res = weaker(res, t.res)
	}
	id, ok := r.classMember(owner, seg, map[string]bool{})
	if !ok {
		return symbol{}, false
	}
	return symbol{nodeID: id, res: res}, true
}

func (r *run) classMember(classID, name string, visited map[string]bool) (string, bool) {
	if visited[classID] {
		return "", false
	}
	visited[classID] = true
	if id, ok := r.members[classID][name]; ok {
		return id, true
	}
	for _, parent := range r.extends[classID] {
		if id, ok := r.classMember(parent, name, visited); ok {
			return id, true
		}
	}
	return "", false
}

// materialize devolve o ID do node de destino, criando Package/External sob demanda
func (r *run) materialize(sym symbol) string {
	switch {
	case sym.nodeID != "":
		return sym.nodeID
	case sym.module != nil:
		return sym.module.fg.File.ID()
	case sym.pkg != "":
		id := "pkg:" + sym.pkg
		if !r.created[id] {
			r.created[id] = true
			r.addNode(entity.Package{Base: entity.Base{NodeID: id}, Name: sym.pkg})
		}
		return id
	default:
		id := r.in.App.ID() + "/ext:" + sym.external
		if !r.created[id] {
			r.created[id] = true
			r.addNode(entity.External{Base: entity.Base{NodeID: id}, Name: sym.external})
		}
		return id
	}
}

func (r *run) addNode(n entity.Node) {
	r.graph.Nodes = append(r.graph.Nodes, n)
	r.nodes[n.ID()] = n
}

// addEdge descarta edges sem localização repetidas (IMPORTS, HAS_TYPE, INVOKES...)
func (r *run) addEdge(e entity.Edge) {
	if e.Loc == nil {
		key := string(e.Type) + "|" + e.From + "|" + e.To
		if r.edgeSet[key] {
			return
		}
		r.edgeSet[key] = true
	}
	r.graph.Edges = append(r.graph.Edges, e)
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func weaker(a, b entity.Resolution) entity.Resolution {
	rank := map[entity.Resolution]int{entity.ResolutionExact: 0, entity.ResolutionInferred: 1, entity.ResolutionNameOnly: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func nameOf(n entity.Node) string {
	switch v := n.(type) {
	case entity.Function:
		return v.Name
	case entity.Field:
		return v.Name
	case entity.EnumMember:
		return v.Name
	}
	return ""
}
