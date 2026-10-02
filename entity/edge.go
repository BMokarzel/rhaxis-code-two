package entity

import "encoding/json"

type EdgeType string

const (
	// estrutura
	ContainsEdge     = EdgeType("CONTAINS")      // Application → File; File/Function → Call
	DeclaresEdge     = EdgeType("DECLARES")      // File/Function → declaração
	HasParameterEdge = EdgeType("HAS_PARAMETER") // Function → Parameter {index}
	HasFieldEdge     = EdgeType("HAS_FIELD")     // Class/Interface → Field
	HasMethodEdge    = EdgeType("HAS_METHOD")    // Class/Interface → Function
	HasMemberEdge    = EdgeType("HAS_MEMBER")    // Enum → EnumMember
	ExportsEdge      = EdgeType("EXPORTS")       // File → declaração

	// uso (expressões achatadas em edges)
	ReadsEdge    = EdgeType("READS")    // Function/File/Call → portador de valor
	WritesEdge   = EdgeType("WRITES")   // Function/File → portador de valor {operator}
	ArgumentEdge = EdgeType("ARGUMENT") // Call → portador de valor {index}

	// resolução
	ImportsEdge      = EdgeType("IMPORTS")       // File → File/Package
	CallsEdge        = EdgeType("CALLS")         // Call → Function/External/Package
	InstantiatesEdge = EdgeType("INSTANTIATES")  // Call{new} → Class
	HasTypeEdge      = EdgeType("HAS_TYPE")      // Variable/Parameter/Field → Class/Interface/Enum
	HasTypeArgEdge   = EdgeType("HAS_TYPE_ARG")  // portador genérico → Class/Interface/Enum (arg de type_arguments) {index}
	ReturnsTypeEdge  = EdgeType("RETURNS_TYPE")  // Function → Class/Interface/Enum
	ExtendsEdge      = EdgeType("EXTENDS")       // Class → Class; Interface → Interface
	ImplementsEdge   = EdgeType("IMPLEMENTS")    // Class → Interface

	// fluxo (derivadas)
	InvokesEdge = EdgeType("INVOKES") // Function → Function
	FlowsToEdge = EdgeType("FLOWS_TO")
	NextEdge    = EdgeType("NEXT")
	ThrowsEdge  = EdgeType("THROWS") // Function → Throw (função pode lançar aquele throw)

	// contrato
	ExposesEdge   = EdgeType("EXPOSES")
	HandledByEdge = EdgeType("HANDLED_BY")
	HasParamEdge  = EdgeType("HAS_PARAM")
	BindsEdge     = EdgeType("BINDS")
	RequestsEdge  = EdgeType("REQUESTS")

	// decorator: Call → Class|Function|Parameter|Field
	DecoratesEdge = EdgeType("DECORATES")
)

// Resolution indica a confiança de uma edge de resolução, fluxo ou contrato
type Resolution string

const (
	ResolutionExact    = Resolution("exact")     // escopo/import, sem ambiguidade
	ResolutionInferred = Resolution("inferred")  // tipo inferido, fallback de export, casamento de rota
	ResolutionNameOnly = Resolution("name_only") // casado só pelo nome
)

type Edge struct {
	Type EdgeType `json:"type"`
	From string   `json:"from"`
	To   string   `json:"to"`
	// Index é a posição, quando a ordem importa (ARGUMENT, HAS_PARAMETER)
	Index *int `json:"index,omitempty"`
	// Loc é o local do uso (READS, WRITES, ARGUMENT, CALLS)
	Loc *Location `json:"location,omitempty"`
	// Member é o caminho de membros não resolvido a partir do alvo: `user.email` → "email"
	Member     string     `json:"member,omitempty"`
	Operator   string     `json:"operator,omitempty"`
	Resolution Resolution `json:"resolution,omitempty"`
}

func IntPtr(i int) *int { return &i }

// Graph é um conjunto de nodes e edges, a unidade trocada entre linker e repository
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Properties achata um node em propriedades escalares, no formato esperado por um banco de grafo.
// Location vira startLine, endLine etc. no nível raiz.
func Properties(n Node) map[string]any {
	props := map[string]any{}
	raw, err := json.Marshal(n)
	if err != nil {
		return props
	}
	_ = json.Unmarshal(raw, &props)
	if loc, ok := props["location"].(map[string]any); ok {
		for k, v := range loc {
			props[k] = v
		}
		delete(props, "location")
	}
	props["type"] = string(n.Type())
	return props
}
