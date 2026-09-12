//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"
)

type accountResponse struct {
	QuotaTotal *int64 `json:"quotaTotal"`
	QuotaUsed  *int64 `json:"quotaUsed"`
}

// TestGetAccount_Success cobre o caso feliz. O Dovecot local não tem o
// plugin de quota habilitado (GETQUOTAROOT devolve "BAD ... Unknown
// command" — confirmado manualmente), então quotaTotal/quotaUsed devem
// vir null (degradação graciosa, não erro) — exercita exatamente esse
// caminho, já que não dá pra testar contra este ambiente um servidor
// que efetivamente reporte quota.
func TestGetAccount_Success(t *testing.T) {
	resp := doAccountRequest(t, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	var body accountResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}
	if body.QuotaTotal != nil {
		t.Errorf("quotaTotal = %v, esperado null (Dovecot local sem plugin de quota)", *body.QuotaTotal)
	}
	if body.QuotaUsed != nil {
		t.Errorf("quotaUsed = %v, esperado null (Dovecot local sem plugin de quota)", *body.QuotaUsed)
	}
}

// TestGetAccount_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword.
func TestGetAccount_MissingHeaders(t *testing.T) {
	resp, err := http.Get(server.URL + "/account")
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestGetAccount_InvalidCredentials cobre credenciais IMAP inválidas.
func TestGetAccount_InvalidCredentials(t *testing.T) {
	resp := doAccountRequest(t, testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// doAccountRequest monta a requisição pra GET /account.
func doAccountRequest(t *testing.T, user, password string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/account", nil)
	if err != nil {
		t.Fatalf("montando requisição: %v", err)
	}
	req.Header.Set("imapUser", user)
	req.Header.Set("imapPassword", password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	return resp
}
