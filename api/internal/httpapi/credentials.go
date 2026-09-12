package httpapi

import "net/http"

// credentials são as credenciais IMAP de uma requisição, enviadas nos
// headers imapUser/imapPassword (multi-conta: a API não guarda usuário
// nenhum, só repassa pro servidor IMAP configurado).
type credentials struct {
	user     string
	password string
}

func credentialsFromRequest(r *http.Request) (credentials, bool) {
	user := r.Header.Get("imapUser")
	password := r.Header.Get("imapPassword")
	if user == "" || password == "" {
		return credentials{}, false
	}
	return credentials{user: user, password: password}, true
}
