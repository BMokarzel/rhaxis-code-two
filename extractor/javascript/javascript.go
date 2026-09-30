// Package javascript extrai arquivos JavaScript e TypeScript (incluindo JSX/TSX) com tree-sitter.
//
// Declarações viram nodes; usos de nomes (leituras, escritas, argumentos, chamadas, tipos) viram
// ir.PendingRef para o linker resolver. Expressões compostas são achatadas: `f(a + b)` gera
// ARGUMENT{0} para a e para b, sem nodes de operação.
package javascript

import (
	"context"
	"fmt"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
	tsjavascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tstypescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"github.com/BMokarzel/rhaxis-code-two/entity"
	"github.com/BMokarzel/rhaxis-code-two/ir"
	"github.com/BMokarzel/rhaxis-code-two/source"
)

const (
	LangJavaScript = "javascript"
	LangTypeScript = "typescript"
	LangTSX        = "tsx"
)

type Extractor struct {
	languages map[string]*ts.Language
}

func New() *Extractor {
	return &Extractor{languages: map[string]*ts.Language{
		LangJavaScript: ts.NewLanguage(tsjavascript.Language()),
		LangTypeScript: ts.NewLanguage(tstypescript.LanguageTypescript()),
		LangTSX:        ts.NewLanguage(tstypescript.LanguageTSX()),
	}}
}

func (e *Extractor) Languages() []string {
	return []string{LangJavaScript, LangTypeScript, LangTSX}
}

func (e *Extractor) Extensions() map[string]string {
	return map[string]string{
		".js": LangJavaScript, ".jsx": LangJavaScript, ".mjs": LangJavaScript, ".cjs": LangJavaScript,
		".ts": LangTypeScript, ".mts": LangTypeScript, ".cts": LangTypeScript,
		".tsx": LangTSX,
	}
}

func (e *Extractor) Extract(ctx context.Context, fileID string, f source.File) (*ir.FileGraph, error) {
	lang, ok := e.languages[f.Language]
	if !ok {
		return nil, fmt.Errorf("linguagem não suportada: %s", f.Language)
	}
	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang); err != nil {
		return nil, err
	}
	tree := parser.Parse(f.Content, nil)
	if tree == nil {
		return nil, fmt.Errorf("falha ao parsear %s", f.Path)
	}
	defer tree.Close()

	x := &fileExtractor{
		src:    f.Content,
		fileID: fileID,
		ids:    map[string]bool{},
		scopes: map[string]*ir.Scope{},
		fg: &ir.FileGraph{File: entity.File{
			Base:     entity.Base{NodeID: fileID},
			Path:     f.Path,
			Language: f.Language,
			Hash:     f.Hash,
		}},
	}
	x.fg.FileScopeID = x.newScope("")
	root := &scope{owner: fileID, scope: x.fg.FileScopeID, from: fileID, edge: entity.ReadsEdge}
	for _, child := range named(tree.RootNode()) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		x.walk(child, root)
	}
	for _, id := range x.scopeOrder {
		x.fg.Scopes = append(x.fg.Scopes, *x.scopes[id])
	}
	return x.fg, nil
}

type fileExtractor struct {
	src        []byte
	fileID     string
	fg         *ir.FileGraph
	ids        map[string]bool
	scopes     map[string]*ir.Scope
	scopeOrder []string
	seq        int
}

// scope é o contexto da travessia: quem é o dono, em que escopo léxico estamos e para onde
// apontam os usos encontrados (a função dona com READS, ou uma chamada com ARGUMENT{index})
type scope struct {
	owner   string
	scope   string
	qual    string
	class   *classInfo
	from    string
	edge    entity.EdgeType
	index   *int
	awaited bool
}

func (s *scope) with(from string, edge entity.EdgeType, index *int) *scope {
	c := *s
	c.from, c.edge, c.index, c.awaited = from, edge, index, false
	return &c
}

type classInfo struct {
	id      string
	qual    string
	members map[string]bool
}

type declared struct {
	name string
	id   string
}

// ---------------------------------------------------------------- travessia

func (x *fileExtractor) walk(n *ts.Node, c *scope) {
	x.walkDecl(n, c, false, nil)
}

// walkDecl percorre qualquer node; para declarações, devolve o que foi declarado
func (x *fileExtractor) walkDecl(n *ts.Node, c *scope, exported bool, decorators []*ts.Node) []declared {
	kind := n.Kind()
	if skipKind(kind) {
		return nil
	}
	switch kind {
	case "import_statement":
		x.importStatement(n)
	case "export_statement":
		x.exportStatement(n, c)
	case "function_declaration", "generator_function_declaration":
		id := x.function(n, c, "", entity.FunctionDeclaration, exported, nil, true)
		return []declared{{name: x.text(n.ChildByFieldName("name")), id: id}}
	case "class_declaration", "abstract_class_declaration":
		name := x.text(n.ChildByFieldName("name"))
		return []declared{{name: name, id: x.class(n, c, name, exported, decorators)}}
	case "interface_declaration":
		return x.interfaceDecl(n, c, exported)
	case "enum_declaration":
		return x.enumDecl(n, c, exported)
	case "lexical_declaration", "variable_declaration":
		return x.variables(n, c, exported)
	case "statement_block":
		inner := *c
		inner.scope = x.newScope(c.scope)
		for _, child := range named(n) {
			x.walk(child, &inner)
		}
	case "for_statement":
		inner := *c
		inner.scope = x.newScope(c.scope)
		for _, child := range named(n) {
			x.walk(child, &inner)
		}
	case "for_in_statement":
		x.forIn(n, c)
	case "catch_clause":
		inner := *c
		inner.scope = x.newScope(c.scope)
		if p := n.ChildByFieldName("parameter"); p != nil {
			for _, id := range patternIdentifiers(p) {
				x.variable(id, &inner, entity.VariableCatch, false, nil)
			}
		}
		if body := n.ChildByFieldName("body"); body != nil {
			x.walk(body, &inner)
		}
	case "identifier", "shorthand_property_identifier":
		x.use(c, x.text(n), nil, n)
	case "member_expression":
		if root, path, ok := x.chain(n); ok {
			x.use(c, root, path, n)
			return nil
		}
		if obj := n.ChildByFieldName("object"); obj != nil {
			x.walk(obj, c)
		}
	case "subscript_expression":
		if obj := n.ChildByFieldName("object"); obj != nil {
			x.walk(obj, c)
		}
		if idx := n.ChildByFieldName("index"); idx != nil {
			x.walk(idx, c)
		}
	case "call_expression":
		x.call(n, c, entity.CallPlain)
	case "new_expression":
		x.call(n, c, entity.CallNew)
	case "await_expression":
		inner := *c
		inner.awaited = true
		for _, child := range named(n) {
			x.walk(child, &inner)
		}
	case "assignment_expression":
		x.write(n.ChildByFieldName("left"), "=", c)
		if right := n.ChildByFieldName("right"); right != nil {
			x.walk(right, c)
		}
	case "augmented_assignment_expression":
		op := x.text(n.ChildByFieldName("operator"))
		x.write(n.ChildByFieldName("left"), op, c)
		if right := n.ChildByFieldName("right"); right != nil {
			x.walk(right, c)
		}
	case "update_expression":
		op := "++"
		if strings.Contains(x.text(n), "--") {
			op = "--"
		}
		x.write(n.ChildByFieldName("argument"), op, c)
	case "arrow_function", "function_expression", "function", "generator_function":
		fk := entity.FunctionExpression
		if kind == "arrow_function" {
			fk = entity.FunctionArrow
		}
		id := x.function(n, c, "", fk, false, nil, false)
		x.valueEdge(c, id, n)
	case "class":
		id := x.class(n, c, x.text(n.ChildByFieldName("name")), false, nil)
		x.valueEdge(c, id, n)
	case "pair":
		if key := n.ChildByFieldName("key"); key != nil && key.Kind() == "computed_property_name" {
			x.walk(key, c)
		}
		if v := n.ChildByFieldName("value"); v != nil {
			x.walk(v, c)
		}
	case "labeled_statement":
		if body := n.ChildByFieldName("body"); body != nil {
			x.walk(body, c)
		}
	default:
		for _, child := range named(n) {
			x.walk(child, c)
		}
	}
	return nil
}

func skipKind(kind string) bool {
	switch kind {
	case "comment", "this", "super", "string", "number", "true", "false", "null", "undefined", "regex",
		"property_identifier", "private_property_identifier", "statement_identifier", "hash_bang_line",
		"type_alias_declaration", "ambient_declaration", "import_alias", "debugger_statement",
		"break_statement", "continue_statement", "empty_statement":
		return true
	}
	return isTypeKind(kind)
}

func isTypeKind(kind string) bool {
	switch kind {
	case "type_annotation", "type_arguments", "type_parameters", "type_identifier", "predefined_type",
		"type_predicate_annotation", "asserts_annotation", "omitting_type_annotation", "opting_type_annotation":
		return true
	}
	return strings.HasSuffix(kind, "_type")
}

// ---------------------------------------------------------------- imports e exports

func (x *fileExtractor) importStatement(n *ts.Node) {
	src := unquote(x.text(n.ChildByFieldName("source")))
	loc := x.loc(n)
	var clause *ts.Node
	for _, child := range named(n) {
		switch child.Kind() {
		case "import_clause":
			clause = child
		case "import_require_clause": // import x = require('y')
			if id := firstOfKind(child, "identifier"); id != nil {
				x.fg.Imports = append(x.fg.Imports, ir.Import{Local: x.text(id), Source: unquote(x.text(child.ChildByFieldName("source"))), Kind: ir.ImportNamespace, Loc: loc})
			}
			return
		}
	}
	if clause == nil {
		x.fg.Imports = append(x.fg.Imports, ir.Import{Source: src, Kind: ir.ImportSideEffect, Loc: loc})
		return
	}
	for _, part := range named(clause) {
		switch part.Kind() {
		case "identifier":
			x.fg.Imports = append(x.fg.Imports, ir.Import{Local: x.text(part), Imported: "default", Source: src, Kind: ir.ImportDefault, Loc: loc})
		case "namespace_import":
			if id := firstOfKind(part, "identifier"); id != nil {
				x.fg.Imports = append(x.fg.Imports, ir.Import{Local: x.text(id), Imported: "*", Source: src, Kind: ir.ImportNamespace, Loc: loc})
			}
		case "named_imports":
			for _, spec := range named(part) {
				if spec.Kind() != "import_specifier" {
					continue
				}
				name := x.text(spec.ChildByFieldName("name"))
				local := name
				if alias := spec.ChildByFieldName("alias"); alias != nil {
					local = x.text(alias)
				}
				kind := ir.ImportNamed
				if name == "default" {
					kind = ir.ImportDefault
				}
				x.fg.Imports = append(x.fg.Imports, ir.Import{Local: local, Imported: name, Source: src, Kind: kind, Loc: loc})
			}
		}
	}
}

func (x *fileExtractor) exportStatement(n *ts.Node, c *scope) {
	var decorators []*ts.Node
	isDefault := false
	var clause, namespace *ts.Node
	star := false
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		switch child.Kind() {
		case "decorator":
			decorators = append(decorators, child)
		case "default":
			isDefault = true
		case "export_clause":
			clause = child
		case "namespace_export":
			namespace = child
		case "*":
			star = true
		}
	}
	srcNode := n.ChildByFieldName("source")
	src := unquote(x.text(srcNode))

	if decl := n.ChildByFieldName("declaration"); decl != nil {
		for _, d := range x.walkDecl(decl, c, true, decorators) {
			name := d.name
			if isDefault {
				name = "default"
			}
			x.export(name, d.name, d.id)
		}
		return
	}
	if isDefault {
		value := n.ChildByFieldName("value")
		if value == nil {
			return
		}
		switch value.Kind() {
		case "identifier":
			x.fg.Exports = append(x.fg.Exports, ir.Export{Kind: ir.ExportLocal, Name: "default", Local: x.text(value)})
		case "class":
			id := x.class(value, c, orDefault(x.text(value.ChildByFieldName("name"))), true, decorators)
			x.export("default", "", id)
		case "arrow_function", "function_expression", "function", "generator_function":
			fk := entity.FunctionExpression
			if value.Kind() == "arrow_function" {
				fk = entity.FunctionArrow
			}
			id := x.function(value, c, orDefault(x.text(value.ChildByFieldName("name"))), fk, true, nil, false)
			x.export("default", "", id)
		default:
			x.walk(value, c)
		}
		return
	}
	if clause != nil {
		for _, spec := range named(clause) {
			if spec.Kind() != "export_specifier" {
				continue
			}
			local := x.text(spec.ChildByFieldName("name"))
			name := local
			if alias := spec.ChildByFieldName("alias"); alias != nil {
				name = x.text(alias)
			}
			if srcNode != nil {
				x.fg.Exports = append(x.fg.Exports, ir.Export{Kind: ir.ExportReexport, Name: name, Local: local, Source: src})
			} else {
				x.fg.Exports = append(x.fg.Exports, ir.Export{Kind: ir.ExportLocal, Name: name, Local: local})
			}
		}
		return
	}
	if namespace != nil && srcNode != nil {
		if id := firstOfKind(namespace, "identifier"); id != nil {
			x.fg.Exports = append(x.fg.Exports, ir.Export{Kind: ir.ExportReexport, Name: x.text(id), Local: "*", Source: src})
		}
		return
	}
	if star && srcNode != nil {
		x.fg.Exports = append(x.fg.Exports, ir.Export{Kind: ir.ExportReexportAll, Source: src})
	}
}

func (x *fileExtractor) export(name, local, id string) {
	if id == "" {
		return
	}
	x.fg.Exports = append(x.fg.Exports, ir.Export{Kind: ir.ExportLocal, Name: name, Local: local, NodeID: id})
	x.edge(entity.Edge{Type: entity.ExportsEdge, From: x.fileID, To: id})
}

// ---------------------------------------------------------------- declarações

// function declara uma função e percorre corpo e parâmetros. name vazio usa o campo "name" do node.
func (x *fileExtractor) function(n *ts.Node, c *scope, name string, kind entity.FunctionKind, exported bool, decorators []*ts.Node, register bool) string {
	if name == "" {
		name = x.text(n.ChildByFieldName("name"))
	}
	qual := x.qualify(c.qual, name, n)
	id := x.unique(qual, n)
	fn := entity.Function{
		CodeBase:   x.codeBase(id, n, c.owner),
		Name:       name,
		Kind:       kind,
		Async:      hasToken(n, "async"),
		Static:     hasToken(n, "static"),
		Exported:   exported,
		Decorators: x.texts(append(decorators, childrenOfKind(n, "decorator")...)),
	}
	inner := &scope{owner: id, scope: x.newScope(c.scope), qual: qual, class: c.class, from: id, edge: entity.ReadsEdge}
	if rt := n.ChildByFieldName("return_type"); rt != nil {
		fn.TypeName = typeText(x.text(rt))
		x.typeRef(id, entity.ReturnsTypeEdge, rt, c.scope, false)
	}
	x.node(fn)
	x.edge(entity.Edge{Type: entity.DeclaresEdge, From: c.owner, To: id})
	if register && name != "" {
		x.declare(c.scope, name, id)
	}
	for _, d := range append(decorators, childrenOfKind(n, "decorator")...) {
		x.walk(d, inner)
	}
	if params := n.ChildByFieldName("parameters"); params != nil {
		x.parameters(params, id, inner, kind == entity.FunctionConstructor)
	} else if p := n.ChildByFieldName("parameter"); p != nil { // arrow com um parâmetro sem parênteses
		x.parameter(p, 0, id, inner, false)
	}
	if body := n.ChildByFieldName("body"); body != nil {
		if body.Kind() == "statement_block" {
			for _, child := range named(body) {
				x.walk(child, inner)
			}
		} else {
			x.walk(body, inner)
		}
	}
	return id
}

func (x *fileExtractor) parameters(params *ts.Node, fnID string, c *scope, constructor bool) {
	index := 0
	for _, p := range named(params) {
		if p.Kind() == "decorator" {
			continue
		}
		x.parameter(p, index, fnID, c, constructor)
		index++
	}
}

func (x *fileExtractor) parameter(p *ts.Node, index int, fnID string, c *scope, constructor bool) {
	param := entity.Parameter{Index: index}
	pattern := p
	var typeNode, value *ts.Node
	property := false
	switch p.Kind() {
	case "required_parameter", "optional_parameter":
		pattern = p.ChildByFieldName("pattern")
		typeNode = p.ChildByFieldName("type")
		value = p.ChildByFieldName("value")
		param.Optional = p.Kind() == "optional_parameter"
		property = firstOfKind(p, "accessibility_modifier") != nil || hasToken(p, "readonly")
		param.Decorators = x.texts(childrenOfKind(p, "decorator"))
		for _, d := range childrenOfKind(p, "decorator") {
			x.walk(d, c)
		}
	case "assignment_pattern":
		pattern = p.ChildByFieldName("left")
		value = p.ChildByFieldName("right")
	}
	if pattern == nil {
		return
	}
	if pattern.Kind() == "rest_pattern" {
		param.Variadic = true
		if inner := firstNamed(pattern); inner != nil {
			pattern = inner
		}
	}
	if pattern.Kind() == "this" {
		return
	}
	param.HasDefault = value != nil
	param.Name = x.text(pattern)
	if typeNode != nil {
		param.TypeName = typeText(x.text(typeNode))
	}
	id := x.unique(c.qual+"("+param.Name+")", p)
	param.CodeBase = x.codeBase(id, p, fnID)
	x.node(param)
	x.edge(entity.Edge{Type: entity.HasParameterEdge, From: fnID, To: id, Index: entity.IntPtr(index)})
	for _, ident := range patternIdentifiers(pattern) {
		x.declare(c.scope, x.text(ident), id)
	}
	if typeNode != nil {
		x.typeRef(id, entity.HasTypeEdge, typeNode, c.scope, false)
	}
	if value != nil {
		x.walk(value, c)
	}

	// parameter property do TS: constructor(private readonly repo: Repo) também declara um campo
	if property && constructor && c.class != nil && pattern.Kind() == "identifier" {
		fid := x.unique(c.class.qual+"."+param.Name, p)
		field := entity.Field{
			CodeBase:   x.codeBase(fid, p, c.class.id),
			Name:       param.Name,
			TypeName:   param.TypeName,
			Optional:   param.Optional,
			Decorators: param.Decorators,
		}
		x.node(field)
		x.edge(entity.Edge{Type: entity.HasFieldEdge, From: c.class.id, To: fid})
		loc := x.loc(p)
		x.edge(entity.Edge{Type: entity.WritesEdge, From: fnID, To: fid, Loc: &loc, Operator: "=", Resolution: entity.ResolutionExact})
		c.class.members[param.Name] = true
		if typeNode != nil {
			x.typeRef(fid, entity.HasTypeEdge, typeNode, c.scope, false)
		}
	}
}

func (x *fileExtractor) class(n *ts.Node, c *scope, name string, exported bool, decorators []*ts.Node) string {
	qual := x.qualify(c.qual, name, n)
	id := x.unique(qual, n)
	decorators = append(decorators, childrenOfKind(n, "decorator")...)
	x.node(entity.Class{
		CodeBase:   x.codeBase(id, n, c.owner),
		Name:       name,
		Abstract:   n.Kind() == "abstract_class_declaration" || hasToken(n, "abstract"),
		Exported:   exported,
		Decorators: x.texts(decorators),
	})
	x.edge(entity.Edge{Type: entity.DeclaresEdge, From: c.owner, To: id})
	if name != "" && n.Kind() != "class" {
		x.declare(c.scope, name, id)
	}

	info := &classInfo{id: id, qual: qual, members: map[string]bool{}}
	classScope := x.newScope(c.scope)
	x.declare(classScope, "this", id)
	inner := &scope{owner: id, scope: classScope, qual: qual, class: info, from: id, edge: entity.ReadsEdge}
	for _, d := range decorators {
		x.walk(d, inner)
	}

	if heritage := firstOfKind(n, "class_heritage"); heritage != nil {
		for _, h := range named(heritage) {
			switch h.Kind() {
			case "extends_clause":
				if v := h.ChildByFieldName("value"); v != nil {
					x.heritageRef(id, entity.ExtendsEdge, v, c.scope)
				}
			case "implements_clause":
				for _, t := range named(h) {
					x.typeRef(id, entity.ImplementsEdge, t, c.scope, false)
				}
			default: // JavaScript: class A extends B
				x.heritageRef(id, entity.ExtendsEdge, h, c.scope)
			}
		}
	}

	body := n.ChildByFieldName("body")
	if body == nil {
		return id
	}
	for _, m := range named(body) {
		if name := x.memberName(m); name != "" {
			info.members[name] = true
		}
	}
	var pending []*ts.Node
	for _, m := range named(body) {
		switch m.Kind() {
		case "decorator":
			pending = append(pending, m)
			continue
		case "method_definition", "method_signature", "abstract_method_signature":
			name := x.memberName(m)
			kind := entity.FunctionMethod
			switch {
			case name == "constructor":
				kind = entity.FunctionConstructor
			case hasToken(m, "get"):
				kind = entity.FunctionGetter
			case hasToken(m, "set"):
				kind = entity.FunctionSetter
			}
			mid := x.function(m, inner, name, kind, false, pending, false)
			x.edge(entity.Edge{Type: entity.HasMethodEdge, From: id, To: mid})
		case "public_field_definition", "field_definition":
			x.classField(m, inner, pending)
		case "class_static_block":
			x.walk(m, inner)
		}
		pending = nil
	}
	return id
}

func (x *fileExtractor) classField(m *ts.Node, c *scope, decorators []*ts.Node) {
	name := x.memberName(m)
	value := m.ChildByFieldName("value")
	decorators = append(decorators, childrenOfKind(m, "decorator")...)
	if value != nil && (value.Kind() == "arrow_function" || value.Kind() == "function_expression") {
		mid := x.function(value, c, name, entity.FunctionArrow, false, decorators, false)
		x.edge(entity.Edge{Type: entity.HasMethodEdge, From: c.class.id, To: mid})
		return
	}
	id := x.unique(c.qual+"."+name, m)
	field := entity.Field{
		CodeBase:   x.codeBase(id, m, c.class.id),
		Name:       name,
		Static:     hasToken(m, "static"),
		Optional:   hasToken(m, "?"),
		Decorators: x.texts(decorators),
	}
	typeNode := m.ChildByFieldName("type")
	if typeNode != nil {
		field.TypeName = typeText(x.text(typeNode))
		x.typeRef(id, entity.HasTypeEdge, typeNode, c.scope, false)
	} else if value != nil {
		field.TypeName = x.inferNew(id, value, c.scope)
	}
	x.node(field)
	x.edge(entity.Edge{Type: entity.HasFieldEdge, From: c.class.id, To: id})
	for _, d := range decorators {
		x.walk(d, c)
	}
	if value != nil {
		x.walk(value, c.with(id, entity.ReadsEdge, nil))
	}
}

func (x *fileExtractor) memberName(m *ts.Node) string {
	name := m.ChildByFieldName("name")
	if name == nil {
		name = m.ChildByFieldName("property") // field_definition do JavaScript
	}
	return x.text(name)
}

func (x *fileExtractor) interfaceDecl(n *ts.Node, c *scope, exported bool) []declared {
	name := x.text(n.ChildByFieldName("name"))
	qual := x.qualify(c.qual, name, n)
	id := x.unique(qual, n)
	x.node(entity.Interface{CodeBase: x.codeBase(id, n, c.owner), Name: name, Exported: exported})
	x.edge(entity.Edge{Type: entity.DeclaresEdge, From: c.owner, To: id})
	x.declare(c.scope, name, id)
	if ext := firstOfKind(n, "extends_type_clause"); ext != nil {
		for _, t := range named(ext) {
			x.typeRef(id, entity.ExtendsEdge, t, c.scope, false)
		}
	}
	if body := n.ChildByFieldName("body"); body != nil {
		for _, m := range named(body) {
			mname := x.text(m.ChildByFieldName("name"))
			switch m.Kind() {
			case "property_signature":
				fid := x.unique(qual+"."+mname, m)
				field := entity.Field{CodeBase: x.codeBase(fid, m, id), Name: mname, Optional: hasToken(m, "?")}
				if t := m.ChildByFieldName("type"); t != nil {
					field.TypeName = typeText(x.text(t))
					x.typeRef(fid, entity.HasTypeEdge, t, c.scope, false)
				}
				x.node(field)
				x.edge(entity.Edge{Type: entity.HasFieldEdge, From: id, To: fid})
			case "method_signature":
				inner := &scope{owner: id, scope: c.scope, qual: qual, from: id, edge: entity.ReadsEdge}
				mid := x.function(m, inner, mname, entity.FunctionMethod, false, nil, false)
				x.edge(entity.Edge{Type: entity.HasMethodEdge, From: id, To: mid})
			}
		}
	}
	return []declared{{name: name, id: id}}
}

func (x *fileExtractor) enumDecl(n *ts.Node, c *scope, exported bool) []declared {
	name := x.text(n.ChildByFieldName("name"))
	qual := x.qualify(c.qual, name, n)
	id := x.unique(qual, n)
	x.node(entity.Enum{CodeBase: x.codeBase(id, n, c.owner), Name: name, Exported: exported})
	x.edge(entity.Edge{Type: entity.DeclaresEdge, From: c.owner, To: id})
	x.declare(c.scope, name, id)
	if body := n.ChildByFieldName("body"); body != nil {
		for _, m := range named(body) {
			member := entity.EnumMember{}
			switch m.Kind() {
			case "property_identifier", "string":
				member.Name = unquote(x.text(m))
			case "enum_assignment":
				member.Name = unquote(x.text(m.ChildByFieldName("name")))
				member.Value = x.text(m.ChildByFieldName("value"))
			default:
				continue
			}
			mid := x.unique(qual+"."+member.Name, m)
			member.CodeBase = x.codeBase(mid, m, id)
			x.node(member)
			x.edge(entity.Edge{Type: entity.HasMemberEdge, From: id, To: mid})
		}
	}
	return []declared{{name: name, id: id}}
}

func (x *fileExtractor) variables(n *ts.Node, c *scope, exported bool) []declared {
	kind := entity.VariableVar
	if k := n.ChildByFieldName("kind"); k != nil {
		kind = entity.VariableKind(x.text(k))
	} else if hasToken(n, "const") {
		kind = entity.VariableConst
	} else if hasToken(n, "let") {
		kind = entity.VariableLet
	}
	var out []declared
	for _, d := range named(n) {
		if d.Kind() != "variable_declarator" {
			continue
		}
		nameNode := d.ChildByFieldName("name")
		value := d.ChildByFieldName("value")
		if nameNode == nil {
			continue
		}
		if value != nil && x.requireImport(nameNode, value) {
			continue
		}
		if value != nil && nameNode.Kind() == "identifier" {
			name := x.text(nameNode)
			switch value.Kind() {
			case "arrow_function", "function_expression", "function", "generator_function":
				fk := entity.FunctionExpression
				if value.Kind() == "arrow_function" {
					fk = entity.FunctionArrow
				}
				id := x.function(value, c, name, fk, exported, nil, true)
				out = append(out, declared{name: name, id: id})
				continue
			case "class":
				id := x.class(value, c, name, exported, nil)
				x.declare(c.scope, name, id)
				out = append(out, declared{name: name, id: id})
				continue
			}
		}
		for _, ident := range patternIdentifiers(nameNode) {
			id := x.variable(ident, c, kind, exported, d.ChildByFieldName("type"))
			if nameNode.Kind() == "identifier" && d.ChildByFieldName("type") == nil && value != nil {
				x.setVariableType(id, x.inferNew(id, value, c.scope))
			}
			out = append(out, declared{name: x.text(ident), id: id})
			if value != nil {
				loc := x.loc(ident)
				x.edge(entity.Edge{Type: entity.WritesEdge, From: c.owner, To: id, Loc: &loc, Operator: "=", Resolution: entity.ResolutionExact})
			}
		}
		if value != nil {
			x.walk(value, c)
		}
	}
	return out
}

func (x *fileExtractor) variable(ident *ts.Node, c *scope, kind entity.VariableKind, exported bool, typeNode *ts.Node) string {
	name := x.text(ident)
	id := x.unique(x.qualify(c.qual, name, ident), ident)
	v := entity.Variable{CodeBase: x.codeBase(id, ident, c.owner), Name: name, Kind: kind, Exported: exported}
	if typeNode != nil {
		v.TypeName = typeText(x.text(typeNode))
		x.typeRef(id, entity.HasTypeEdge, typeNode, c.scope, false)
	}
	x.node(v)
	x.edge(entity.Edge{Type: entity.DeclaresEdge, From: c.owner, To: id})
	x.declare(c.scope, name, id)
	return id
}

func (x *fileExtractor) setVariableType(id, typeName string) {
	if typeName == "" {
		return
	}
	for i, n := range x.fg.Nodes {
		if v, ok := n.(entity.Variable); ok && v.NodeID == id {
			v.TypeName = typeName
			x.fg.Nodes[i] = v
			return
		}
	}
}

// inferNew registra HAS_TYPE inferido quando o valor é `new X()` e devolve o nome do tipo
func (x *fileExtractor) inferNew(id string, value *ts.Node, scopeID string) string {
	if value.Kind() != "new_expression" {
		return ""
	}
	ctor := value.ChildByFieldName("constructor")
	root, path, ok := x.chain(ctor)
	if !ok {
		return ""
	}
	x.fg.Refs = append(x.fg.Refs, ir.PendingRef{From: id, Edge: entity.HasTypeEdge, ScopeID: scopeID, Name: root, Path: path, Loc: x.loc(ctor), Inferred: true})
	return x.text(ctor)
}

// requireImport trata `const x = require('y')` e `const { a } = require('y')` como import
func (x *fileExtractor) requireImport(nameNode, value *ts.Node) bool {
	src, ok := x.requireSource(value)
	if !ok {
		return false
	}
	loc := x.loc(value)
	switch nameNode.Kind() {
	case "identifier":
		x.fg.Imports = append(x.fg.Imports, ir.Import{Local: x.text(nameNode), Imported: "*", Source: src, Kind: ir.ImportNamespace, Loc: loc})
	case "object_pattern":
		for _, p := range named(nameNode) {
			switch p.Kind() {
			case "shorthand_property_identifier_pattern":
				name := x.text(p)
				x.fg.Imports = append(x.fg.Imports, ir.Import{Local: name, Imported: name, Source: src, Kind: ir.ImportNamed, Loc: loc})
			case "pair_pattern":
				key, val := p.ChildByFieldName("key"), p.ChildByFieldName("value")
				if val != nil && val.Kind() == "identifier" {
					x.fg.Imports = append(x.fg.Imports, ir.Import{Local: x.text(val), Imported: x.text(key), Source: src, Kind: ir.ImportNamed, Loc: loc})
				}
			}
		}
	default:
		return false
	}
	return true
}

func (x *fileExtractor) requireSource(n *ts.Node) (string, bool) {
	if n == nil || n.Kind() != "call_expression" {
		return "", false
	}
	fn := n.ChildByFieldName("function")
	if fn == nil || fn.Kind() != "identifier" || x.text(fn) != "require" {
		return "", false
	}
	args := named(n.ChildByFieldName("arguments"))
	if len(args) != 1 || args[0].Kind() != "string" {
		return "", false
	}
	return unquote(x.text(args[0])), true
}

func (x *fileExtractor) forIn(n *ts.Node, c *scope) {
	inner := *c
	inner.scope = x.newScope(c.scope)
	left := n.ChildByFieldName("left")
	if right := n.ChildByFieldName("right"); right != nil {
		x.walk(right, c)
	}
	if left != nil {
		if n.ChildByFieldName("kind") != nil {
			for _, id := range patternIdentifiers(left) {
				x.variable(id, &inner, entity.VariableLoop, false, nil)
			}
		} else {
			x.write(left, "=", &inner)
		}
	}
	if body := n.ChildByFieldName("body"); body != nil {
		x.walk(body, &inner)
	}
}

// ---------------------------------------------------------------- usos

func (x *fileExtractor) call(n *ts.Node, c *scope, kind entity.CallKind) {
	if src, ok := x.requireSource(n); ok {
		x.fg.Imports = append(x.fg.Imports, ir.Import{Source: src, Kind: ir.ImportSideEffect, Loc: x.loc(n)})
		return
	}
	field := "function"
	if kind == entity.CallNew {
		field = "constructor"
	}
	callee := n.ChildByFieldName(field)
	calleeText := x.text(callee)
	if len(calleeText) > 200 {
		calleeText = calleeText[:200]
	}
	id := x.unique(fmt.Sprintf("%s/call@%d:%d", c.qual, n.StartPosition().Row+1, n.StartPosition().Column+1), n)
	x.node(entity.Call{CodeBase: x.codeBase(id, n, c.owner), Kind: kind, Awaited: c.awaited, CalleeText: calleeText})
	x.edge(entity.Edge{Type: entity.ContainsEdge, From: c.owner, To: id})
	x.valueEdge(c, id, n)

	inner := c.with(id, entity.ReadsEdge, nil)
	if callee != nil {
		if root, path, ok := x.chain(callee); ok {
			if root != "super" && !(root == "this" && len(path) == 0) {
				edge := entity.CallsEdge
				if kind == entity.CallNew {
					edge = entity.InstantiatesEdge
				}
				x.fg.Refs = append(x.fg.Refs, ir.PendingRef{From: id, Edge: edge, ScopeID: c.scope, Name: root, Path: path, Loc: x.loc(callee)})
			}
		} else {
			x.walk(callee, inner)
		}
	}
	for i, arg := range named(n.ChildByFieldName("arguments")) {
		x.walk(arg, c.with(id, entity.ArgumentEdge, entity.IntPtr(i)))
	}
}

// valueEdge liga um node de valor (chamada, função anônima) a quem o contém quando ele é
// argumento ou parte de outra chamada: f(g(x)) → f -ARGUMENT{0}-> g
func (x *fileExtractor) valueEdge(c *scope, id string, n *ts.Node) {
	if c.from == c.owner || c.from == id {
		return
	}
	loc := x.loc(n)
	x.edge(entity.Edge{Type: c.edge, From: c.from, To: id, Index: c.index, Loc: &loc, Resolution: entity.ResolutionExact})
}

func (x *fileExtractor) use(c *scope, root string, path []string, n *ts.Node) {
	if root == "super" || (root == "this" && len(path) == 0) || root == "undefined" {
		return
	}
	x.fg.Refs = append(x.fg.Refs, ir.PendingRef{From: c.from, Edge: c.edge, ScopeID: c.scope, Name: root, Path: path, Index: c.index, Loc: x.loc(n)})
}

func (x *fileExtractor) write(left *ts.Node, op string, c *scope) {
	if left == nil {
		return
	}
	if root, path, ok := x.chain(left); ok {
		// JavaScript: `this.value = x` dentro da classe declara o campo
		if root == "this" && len(path) == 1 && c.class != nil && !c.class.members[path[0]] {
			fid := x.unique(c.class.qual+"."+path[0], left)
			x.node(entity.Field{CodeBase: x.codeBase(fid, left, c.class.id), Name: path[0]})
			x.edge(entity.Edge{Type: entity.HasFieldEdge, From: c.class.id, To: fid})
			c.class.members[path[0]] = true
		}
		x.fg.Refs = append(x.fg.Refs, ir.PendingRef{From: c.owner, Edge: entity.WritesEdge, ScopeID: c.scope, Name: root, Path: path, Loc: x.loc(left), Operator: op})
		return
	}
	switch left.Kind() {
	case "object_pattern", "array_pattern":
		for _, ident := range patternIdentifiers(left) {
			x.fg.Refs = append(x.fg.Refs, ir.PendingRef{From: c.owner, Edge: entity.WritesEdge, ScopeID: c.scope, Name: x.text(ident), Loc: x.loc(ident), Operator: op})
		}
	case "subscript_expression":
		x.write(left.ChildByFieldName("object"), op, c)
		if idx := left.ChildByFieldName("index"); idx != nil {
			x.walk(idx, c)
		}
	case "parenthesized_expression", "non_null_expression", "as_expression":
		x.write(firstNamed(left), op, c)
	default:
		x.walk(left, c)
	}
}

// chain reduz `a.b.c`, `this.x.y` e `a?.b` a raiz + caminho
func (x *fileExtractor) chain(n *ts.Node) (string, []string, bool) {
	if n == nil {
		return "", nil, false
	}
	switch n.Kind() {
	case "identifier", "this", "super":
		return x.text(n), nil, true
	case "non_null_expression":
		return x.chain(firstNamed(n))
	case "member_expression":
		prop := n.ChildByFieldName("property")
		if prop == nil {
			return "", nil, false
		}
		root, path, ok := x.chain(n.ChildByFieldName("object"))
		if !ok {
			return "", nil, false
		}
		return root, append(append([]string{}, path...), x.text(prop)), true
	}
	return "", nil, false
}

// typeRef registra uma referência de tipo; tipos primitivos, uniões e literais são ignorados
func (x *fileExtractor) typeRef(from string, edge entity.EdgeType, t *ts.Node, scopeID string, inferred bool) {
	t = unwrapType(t)
	if t == nil {
		return
	}
	switch t.Kind() {
	case "type_identifier", "identifier":
		x.fg.Refs = append(x.fg.Refs, ir.PendingRef{From: from, Edge: edge, ScopeID: scopeID, Name: x.text(t), Loc: x.loc(t), Inferred: inferred})
	case "nested_type_identifier", "member_expression":
		parts := strings.Split(x.text(t), ".")
		x.fg.Refs = append(x.fg.Refs, ir.PendingRef{From: from, Edge: edge, ScopeID: scopeID, Name: parts[0], Path: parts[1:], Loc: x.loc(t), Inferred: inferred})
	case "generic_type":
		name := t.ChildByFieldName("name")
		switch x.text(name) {
		case "Promise", "Observable", "Array", "ReadonlyArray", "Partial", "Readonly", "Required":
			if args := firstOfKind(t, "type_arguments"); args != nil {
				if first := firstNamed(args); first != nil {
					x.typeRef(from, edge, first, scopeID, inferred)
				}
			}
			return
		}
		x.typeRef(from, edge, name, scopeID, inferred)
	case "array_type":
		x.typeRef(from, edge, firstNamed(t), scopeID, inferred)
	}
}

func (x *fileExtractor) heritageRef(from string, edge entity.EdgeType, v *ts.Node, scopeID string) {
	if root, path, ok := x.chain(v); ok {
		x.fg.Refs = append(x.fg.Refs, ir.PendingRef{From: from, Edge: edge, ScopeID: scopeID, Name: root, Path: path, Loc: x.loc(v)})
		return
	}
	// extends Base<T> ou extends mixin(Base)
	if v.Kind() == "call_expression" {
		x.heritageRef(from, edge, v.ChildByFieldName("function"), scopeID)
	}
}

func unwrapType(t *ts.Node) *ts.Node {
	for t != nil {
		switch t.Kind() {
		case "type_annotation", "parenthesized_type", "readonly_type":
			t = firstNamed(t)
		default:
			return t
		}
	}
	return nil
}

// ---------------------------------------------------------------- utilitários

func (x *fileExtractor) newScope(parent string) string {
	x.seq++
	id := fmt.Sprintf("%s#scope%d", x.fileID, x.seq)
	x.scopes[id] = &ir.Scope{ID: id, ParentID: parent, Names: map[string]string{}}
	x.scopeOrder = append(x.scopeOrder, id)
	return id
}

func (x *fileExtractor) declare(scopeID, name, id string) {
	if name == "" {
		return
	}
	x.scopes[scopeID].Names[name] = id
}

func (x *fileExtractor) qualify(parent, name string, n *ts.Node) string {
	if name == "" {
		name = fmt.Sprintf("fn@%d:%d", n.StartPosition().Row+1, n.StartPosition().Column+1)
	}
	if parent == "" {
		return name
	}
	return parent + "." + name
}

// unique gera o ID do node. Declarações usam o nome qualificado (estável entre edições);
// colisões recebem a linha como sufixo.
func (x *fileExtractor) unique(qual string, n *ts.Node) string {
	id := x.fileID + "#" + qual
	if x.ids[id] {
		id = fmt.Sprintf("%s@%d", id, n.StartPosition().Row+1)
		for i := 2; x.ids[id]; i++ {
			id = fmt.Sprintf("%s#%d", id, i)
		}
	}
	x.ids[id] = true
	return id
}

func (x *fileExtractor) codeBase(id string, n *ts.Node, owner string) entity.CodeBase {
	return entity.CodeBase{Base: entity.Base{NodeID: id}, Loc: x.loc(n), OwnerID: owner}
}

func (x *fileExtractor) loc(n *ts.Node) entity.Location {
	s, e := n.StartPosition(), n.EndPosition()
	return entity.Location{
		FileID:    x.fileID,
		StartLine: int(s.Row) + 1, StartCol: int(s.Column) + 1,
		EndLine: int(e.Row) + 1, EndCol: int(e.Column) + 1,
	}
}

func (x *fileExtractor) node(n entity.Node) { x.fg.Nodes = append(x.fg.Nodes, n) }
func (x *fileExtractor) edge(e entity.Edge) { x.fg.Edges = append(x.fg.Edges, e) }

func (x *fileExtractor) text(n *ts.Node) string {
	if n == nil {
		return ""
	}
	return n.Utf8Text(x.src)
}

func (x *fileExtractor) texts(nodes []*ts.Node) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, strings.TrimPrefix(x.text(n), "@"))
	}
	return out
}

func named(n *ts.Node) []*ts.Node {
	if n == nil {
		return nil
	}
	out := make([]*ts.Node, 0, n.NamedChildCount())
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if child := n.NamedChild(i); child != nil && child.Kind() != "comment" {
			out = append(out, child)
		}
	}
	return out
}

func firstNamed(n *ts.Node) *ts.Node {
	if children := named(n); len(children) > 0 {
		return children[0]
	}
	return nil
}

func firstOfKind(n *ts.Node, kind string) *ts.Node {
	for _, child := range named(n) {
		if child.Kind() == kind {
			return child
		}
	}
	return nil
}

func childrenOfKind(n *ts.Node, kind string) []*ts.Node {
	var out []*ts.Node
	for _, child := range named(n) {
		if child.Kind() == kind {
			out = append(out, child)
		}
	}
	return out
}

// hasToken procura um filho direto (inclusive tokens anônimos como async, static, get)
func hasToken(n *ts.Node, token string) bool {
	for i := uint(0); i < n.ChildCount(); i++ {
		if child := n.Child(i); child != nil && child.Kind() == token {
			return true
		}
	}
	return false
}

// patternIdentifiers devolve os identificadores declarados por um padrão (a, {a, b: c}, [a, ...b])
func patternIdentifiers(n *ts.Node) []*ts.Node {
	if n == nil {
		return nil
	}
	switch n.Kind() {
	case "identifier", "shorthand_property_identifier_pattern":
		return []*ts.Node{n}
	case "pair_pattern":
		return patternIdentifiers(n.ChildByFieldName("value"))
	case "assignment_pattern", "object_assignment_pattern":
		return patternIdentifiers(n.ChildByFieldName("left"))
	case "object_pattern", "array_pattern", "rest_pattern":
		var out []*ts.Node
		for _, child := range named(n) {
			out = append(out, patternIdentifiers(child)...)
		}
		return out
	}
	return nil
}

func typeText(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), ":"))
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'' || s[0] == '`') {
		return s[1 : len(s)-1]
	}
	return s
}

func orDefault(name string) string {
	if name == "" {
		return "default"
	}
	return name
}
