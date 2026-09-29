package node

type Node interface{}

// Code é um node de código, ele possui uma localização dentro de um projeto
type Code interface {
	Location() *Location
}

// Representa o local onde o projeto é armazenado, podendo ser um repositorio github, bitbucket, etc.
type Repository struct{}

// Representa o projeto como um todo, a aplicação a ser executada
type Application struct{}

// Representa o endpoint, a API, não é um node de código, é uma abstração
type Endpoint struct{}

type Function struct {
	ID       string
	Name     string
	Async    bool
	Location Location
}

// o node de call chama uma função
type Call struct {
	ID       string
	Location Location
}

type If struct {
	Condition string
}

type Else struct{}

type While struct {
	Condition string
}

type Try struct{}

type Catch struct{}

// Bloco final de execução de uma função, pode estar ligado a uma função ou variavel
type Return struct{}

// Bloco final de execução de uma função, geralmente ligado a u
type Throw struct{}

type Location struct{}

type Variable struct{}

type Constant struct{}

type Enum struct{}

// Pode ser um dado primitivo, como string, boolean, etc. Ou algo que derive de uma estrutura primitiva. Um objeto pode apontar para um dado x, que por sua vez esta vinculado a um objeto primitivo
type Data struct{}

type Interface struct{}

// Um objeto é uma estrutura de dado complexa, composta por outros objetos, dados compostos ou estruturas de dados primitivas como strings, booleans, etc
// Um objeto pode estar ligado a funções, que nesse caso seriam os metodos deste objeto
type Object struct{}
