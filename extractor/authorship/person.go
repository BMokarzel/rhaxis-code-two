package authorship

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/BMokarzel/rhaxis-code-two/entity"
)

// identity é o par normalizado (email, nome) depois de aplicar .mailmap.
// Note que .mailmap é aplicado pelo próprio git (log/blame) via --use-mailmap,
// então aqui só precisamos normalizar case + espaços.
type identity struct {
	Name  string
	Email string // já em lowercase, trimado
}

func normalizeIdentity(name, email string) identity {
	return identity{
		Name:  strings.TrimSpace(name),
		Email: strings.ToLower(strings.TrimSpace(email)),
	}
}

// personID é determinístico por email normalizado. Dois commits do mesmo autor
// (mesmo email) sempre colapsam no mesmo Person — mesmo que o nome varie.
// Email vazio cai para "anon-<hash do nome>".
func personID(id identity) string {
	key := id.Email
	if key == "" {
		key = "anon-" + id.Name
	}
	sum := sha1.Sum([]byte(key))
	return "person:" + hex.EncodeToString(sum[:])
}

// toPerson monta o node Person respeitando a política de persistência de email.
func toPerson(id identity, store StoreEmail) entity.Person {
	p := entity.Person{
		Base: entity.Base{NodeID: personID(id)},
		Name: id.Name,
	}
	switch store {
	case StoreEmailPlain, "":
		p.Email = id.Email
	case StoreEmailHash:
		if id.Email != "" {
			sum := sha256.Sum256([]byte(id.Email))
			p.Email = "sha256:" + hex.EncodeToString(sum[:])
		}
	case StoreEmailNone:
		// deixa vazio
	}
	return p
}
