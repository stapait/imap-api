//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"
)

type folderInfo struct {
	Name     string `json:"name"`
	Messages uint32 `json:"messages"`
	Unread   uint32 `json:"unread"`
}

type foldersResponse struct {
	Folders []folderInfo `json:"folders"`
}

// TestGetFolders_Success cobre o caso feliz: com credenciais válidas,
// a resposta deve incluir pelo menos as pastas padrão que o Dovecot
// cria pra todo usuário novo. Outras pastas também podem aparecer.
func TestGetFolders_Success(t *testing.T) {
	resp := doFoldersRequest(t, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	var body foldersResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}

	names := make(map[string]bool, len(body.Folders))
	for _, f := range body.Folders {
		names[f.Name] = true
	}

	for _, expected := range []string{"INBOX", "Drafts", "Junk", "Sent", "Trash"} {
		if !names[expected] {
			t.Errorf("pasta padrão do Dovecot %q não encontrada na resposta: %+v", expected, body.Folders)
		}
	}
}

// TestGetFolders_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword — deve ser rejeitada com 401, sem nem tentar
// falar com o IMAP.
func TestGetFolders_MissingHeaders(t *testing.T) {
	resp, err := http.Get(server.URL + "/folders")
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestGetFolders_InvalidCredentials cobre credenciais IMAP inválidas
// — a autenticação no Dovecot deve falhar e a API deve reportar 401.
func TestGetFolders_InvalidCredentials(t *testing.T) {
	resp := doFoldersRequest(t, testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func doFoldersRequest(t *testing.T, user, password string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/folders", nil)
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
