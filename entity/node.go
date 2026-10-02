package entity

type NodeType string

const (
	// estrutura
	RepositoryNode  = NodeType("Repository")
	ApplicationNode = NodeType("Application")
	ModuleNode      = NodeType("Module")
	FileNode        = NodeType("File")
	PackageNode     = NodeType("Package")
	ExternalNode    = NodeType("External")

	// declarações
	FunctionNode   = NodeType("Function")
	ParameterNode  = NodeType("Parameter")
	VariableNode   = NodeType("Variable")
	ClassNode      = NodeType("Class")
	InterfaceNode  = NodeType("Interface")
	FieldNode      = NodeType("Field")
	EnumNode       = NodeType("Enum")
	EnumMemberNode = NodeType("EnumMember")

	// código
	CallNode       = NodeType("Call")
	AssignmentNode = NodeType("Assignment")
	ReturnNode     = NodeType("Return")
	ThrowNode      = NodeType("Throw")
	IfNode         = NodeType("If")
	LoopNode       = NodeType("Loop")
	SwitchNode     = NodeType("Switch")
	CaseNode       = NodeType("Case")
	TryNode        = NodeType("Try")
	CatchNode      = NodeType("Catch")
	JumpNode       = NodeType("Jump")

	// contrato
	EndpointNode  = NodeType("Endpoint")
	HttpParamNode = NodeType("HttpParam")

	// autoria
	CommitNode = NodeType("Commit")
	PersonNode = NodeType("Person")
	TeamNode   = NodeType("Team")

	// cross-service
	TopicNode      = NodeType("Topic")
	LinkReviewNode = NodeType("LinkReview")
)

type Node interface {
	ID() string
	Type() NodeType
}

// Code é um node de código, ele possui uma localização dentro de um projeto
type Code interface {
	Node
	Location() Location
	Owner() string
}

// Base é embutido por todo node
type Base struct {
	NodeID string `json:"id"`
}

func (b Base) ID() string { return b.NodeID }

// CodeBase é embutido por todo node que vem do código-fonte
type CodeBase struct {
	Base
	Loc Location `json:"location"`
	// OwnerID é a função (ou o file) que contém o node
	OwnerID string `json:"ownerId,omitempty"`
}

func (c CodeBase) Location() Location { return c.Loc }
func (c CodeBase) Owner() string      { return c.OwnerID }

// Não é um node, é uma estrutura de metadado anexada aos nodes de codigo.
// Linhas e colunas começam em 1.
type Location struct {
	FileID    string `json:"fileId"`
	StartLine int    `json:"startLine"`
	StartCol  int    `json:"startCol"`
	EndLine   int    `json:"endLine"`
	EndCol    int    `json:"endCol"`
}

// Representa o local onde o projeto é armazenado, podendo ser um repositorio github, bitbucket, etc.
// Possui edge CONTAINS apontando para aplicações
type Repository struct {
	Base
	URL      string `json:"url"`
	Provider string `json:"provider"`
}

func (Repository) Type() NodeType { return RepositoryNode }

// Representa o projeto como um todo, a aplicação a ser executada
// Possui edges CONTAINS apontando para files, EXPOSES para endpoints
type Application struct {
	Base
	Name string `json:"name"`
	Key  string `json:"key"`
	// Sinais denormalizados pelo passo crosservice/signals para lookup O(1) no
	// linker cross-service. Cada entrada em Endpoints é "METHOD PATH" (ex.:
	// "GET /users/:id"). EnvVarsRead lista nomes de env (`process.env.X` → "X").
	// HostCandidates lista hosts literais que o próprio código menciona em URLs
	// (ex.: "users:8080" ou "api.internal"). Opcionais, podem vir vazios.
	Endpoints      []string `json:"endpoints,omitempty"`
	EnvVarsRead    []string `json:"envVarsRead,omitempty"`
	HostCandidates []string `json:"hostCandidates,omitempty"`
}

func (Application) Type() NodeType { return ApplicationNode }

// Agrupa files (pacote/diretório)
type Module struct {
	Base
	Name string `json:"name"`
	Path string `json:"path"`
}

func (Module) Type() NodeType { return ModuleNode }

// Unidade de extração e porta de entrada de um diff
type File struct {
	Base
	Name     string `json:"name"` // basename do path, para navegação
	Path     string `json:"path"`
	Language string `json:"language"`
	Hash     string `json:"hash"`
}

func (File) Type() NodeType { return FileNode }

// Dependência externa (node:fs, axios, @nestjs/common).
// O ID é global (não pertence a uma aplicação) para permitir PROVIDED_BY.
type Package struct {
	Base
	Name string `json:"name"`
}

func (Package) Type() NodeType { return PackageNode }

// Nome que não resolve para nenhuma declaração nem import (console, process, JSON...)
type External struct {
	Base
	Name string `json:"name"`
}

func (External) Type() NodeType { return ExternalNode }

type FunctionKind string

const (
	FunctionDeclaration = FunctionKind("declaration")
	FunctionExpression  = FunctionKind("expression")
	FunctionArrow       = FunctionKind("arrow")
	FunctionMethod      = FunctionKind("method")
	FunctionConstructor = FunctionKind("constructor")
	FunctionGetter      = FunctionKind("getter")
	FunctionSetter      = FunctionKind("setter")
)

// Funções, métodos, arrows e funções anônimas
type Function struct {
	CodeBase
	Name       string       `json:"name"`
	Kind       FunctionKind `json:"kind"`
	Async      bool         `json:"async"`
	Static     bool         `json:"static,omitempty"`
	Abstract   bool         `json:"abstract,omitempty"`
	Override   bool         `json:"override,omitempty"`
	Private    bool         `json:"private,omitempty"`
	Protected  bool         `json:"protected,omitempty"`
	Exported   bool         `json:"exported"`
	TypeName   string       `json:"typeName,omitempty"` // tipo de retorno, texto bruto
	Decorators []string     `json:"decorators,omitempty"`
}

func (Function) Type() NodeType { return FunctionNode }

// Declaração do parâmetro dentro da função; é a junção do fluxo entre funções
type Parameter struct {
	CodeBase
	Name       string   `json:"name"`
	Index      int      `json:"index"`
	Variadic   bool     `json:"variadic,omitempty"`
	HasDefault bool     `json:"hasDefault,omitempty"`
	Optional   bool     `json:"optional,omitempty"`
	Readonly   bool     `json:"readonly,omitempty"`
	Private    bool     `json:"private,omitempty"`
	Protected  bool     `json:"protected,omitempty"`
	TypeName   string   `json:"typeName,omitempty"`
	Decorators []string `json:"decorators,omitempty"`
}

func (Parameter) Type() NodeType { return ParameterNode }

type VariableKind string

const (
	VariableConst = VariableKind("const")
	VariableLet   = VariableKind("let")
	VariableVar   = VariableKind("var")
	VariableCatch = VariableKind("catch")
	VariableLoop  = VariableKind("loop")
)

// A declaração é um node único; cada uso é uma edge (READS, WRITES, ARGUMENT) para ele
type Variable struct {
	CodeBase
	Name     string       `json:"name"`
	Kind     VariableKind `json:"kind"`
	Exported bool         `json:"exported"`
	TypeName string       `json:"typeName,omitempty"`
}

func (Variable) Type() NodeType { return VariableNode }

type Class struct {
	CodeBase
	Name       string   `json:"name"`
	Abstract   bool     `json:"abstract,omitempty"`
	Exported   bool     `json:"exported"`
	Decorators []string `json:"decorators,omitempty"`
}

func (Class) Type() NodeType { return ClassNode }

type Interface struct {
	CodeBase
	Name     string `json:"name"`
	Exported bool   `json:"exported"`
}

func (Interface) Type() NodeType { return InterfaceNode }

// Campo de Class ou Interface
type Field struct {
	CodeBase
	Name       string   `json:"name"`
	TypeName   string   `json:"typeName,omitempty"`
	Static     bool     `json:"static,omitempty"`
	Optional   bool     `json:"optional,omitempty"`
	Readonly   bool     `json:"readonly,omitempty"`
	Private    bool     `json:"private,omitempty"`
	Protected  bool     `json:"protected,omitempty"`
	Abstract   bool     `json:"abstract,omitempty"`
	Decorators []string `json:"decorators,omitempty"`
}

func (Field) Type() NodeType { return FieldNode }

type Enum struct {
	CodeBase
	Name     string `json:"name"`
	Exported bool   `json:"exported"`
}

func (Enum) Type() NodeType { return EnumNode }

type EnumMember struct {
	CodeBase
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

func (EnumMember) Type() NodeType { return EnumMemberNode }

type CallKind string

const (
	CallPlain = CallKind("call")
	CallNew   = CallKind("new")
)

// Uma chamada. Os argumentos são edges ARGUMENT{index}; o alvo é a edge CALLS
type Call struct {
	CodeBase
	Name       string   `json:"name"` // igual a CalleeText, para navegação no Neo4j Browser
	Kind       CallKind `json:"kind"`
	Awaited    bool     `json:"awaited,omitempty"`
	CalleeText string   `json:"calleeText"`
}

func (Call) Type() NodeType { return CallNode }

// Statements abaixo fazem parte do modelo mas ainda não são extraídos (ver docs/architecture.md).

type Assignment struct {
	CodeBase
	Operator string `json:"operator"`
}

func (Assignment) Type() NodeType { return AssignmentNode }

type Return struct{ CodeBase }

func (Return) Type() NodeType { return ReturnNode }

type Throw struct{ CodeBase }

func (Throw) Type() NodeType { return ThrowNode }

type If struct {
	CodeBase
	ConditionText string `json:"conditionText"`
}

func (If) Type() NodeType { return IfNode }

type Loop struct {
	CodeBase
	Kind string `json:"kind"` // while, do_while, for, for_in, for_of
}

func (Loop) Type() NodeType { return LoopNode }

type Switch struct{ CodeBase }

func (Switch) Type() NodeType { return SwitchNode }

type Case struct {
	CodeBase
	IsDefault bool `json:"isDefault"`
}

func (Case) Type() NodeType { return CaseNode }

type Try struct{ CodeBase }

func (Try) Type() NodeType { return TryNode }

type Catch struct{ CodeBase }

func (Catch) Type() NodeType { return CatchNode }

// Jump representa `break` ou `continue`, com label opcional (ex.: `break outer`).
// Kind é "break" ou "continue". Permite rastrear saída de loop/switch quando houver NEXT.
type Jump struct {
	CodeBase
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
}

func (Jump) Type() NodeType { return JumpNode }

// Representa o endpoint, a API, não é um node de código, é uma abstração
// Possui edge HANDLED_BY apontando para a função handler
type Endpoint struct {
	Base
	Method string `json:"method"`
	Path   string `json:"path"` // normalizado: /users/{id}
}

func (Endpoint) Type() NodeType { return EndpointNode }

// Parâmetro do contrato HTTP (convenção OpenAPI)
type HttpParam struct {
	Base
	Name     string `json:"name"`
	In       string `json:"in"` // path, query, header, body, cookie
	TypeName string `json:"typeName,omitempty"`
	Required bool   `json:"required"`
}

func (HttpParam) Type() NodeType { return HttpParamNode }

// Futuro: node Telemetry{Kind: log, span; Template} ligado por EMITS a partir do Call que emite.

// Commit representa um commit do git. ID global ("commit:<sha>"), compartilhado entre apps
// que vivem no mesmo repo. Persistido fora do prefixo <appKey:>, portanto sobrevive à
// reextração da aplicação.
type Commit struct {
	Base
	SHA         string `json:"sha"`
	Message     string `json:"message"`
	AuthoredAt  string `json:"authoredAt"`  // RFC3339
	CommittedAt string `json:"committedAt"` // RFC3339
	Parent      string `json:"parent,omitempty"`
}

func (Commit) Type() NodeType { return CommitNode }

// Person representa a identidade de um autor/committer. ID global
// ("person:<sha1(email_normalizado)>"). Email pode ser gravado em plano, hash ou vazio
// conforme AuthorshipConfig.StoreEmail (plain|hash|none).
type Person struct {
	Base
	Name        string `json:"name"`
	Email       string `json:"email,omitempty"`
	GithubLogin string `json:"githubLogin,omitempty"`
}

func (Person) Type() NodeType { return PersonNode }

// Team é um grupo nomeado de Person. ID global ("team:<slug>").
type Team struct {
	Base
	Name    string   `json:"name"`
	Members []string `json:"members,omitempty"`
}

func (Team) Type() NodeType { return TeamNode }

// Topic representa uma fila/stream (Kafka, RabbitMQ, SNS, SQS, Redis, ...).
// ID global ("topic:<broker>:<name>") para que PRODUCES/CONSUMES de apps
// diferentes convirjam no mesmo node. Broker "unknown" é aceito quando o
// resolver só identificou o nome mas não detectou o cliente.
type Topic struct {
	Base
	Name   string `json:"name"`
	Broker string `json:"broker"`
}

func (Topic) Type() NodeType { return TopicNode }

// LinkReview é um item de revisão humana gerado quando os resolvers de
// cross-service encontram mais de um candidato forte. ID escopado pela app
// de origem ("<appKey>:review:<sha1 do call site>") para ser removido junto
// com a reextração dessa app.
//
// Candidatos são armazenados como três slices paralelos de escalares para
// casar com o modelo de properties do Neo4j (sem lists of maps). Len dos três
// deve ser igual; usar NewLinkReview para construir corretamente.
type LinkReview struct {
	Base
	CallID           string   `json:"callId"`
	EdgeType         EdgeType `json:"edgeType"`
	ChosenID         string   `json:"chosenId"`
	CandidateTargets []string `json:"candidateTargets,omitempty"`
	CandidateScores  []int    `json:"candidateScores,omitempty"`
	CandidateWhys    []string `json:"candidateWhys,omitempty"`
	Hint             string   `json:"hint,omitempty"`
	Status           string   `json:"status"` // pending|confirmed|rejected|overridden
}

func (LinkReview) Type() NodeType { return LinkReviewNode }
