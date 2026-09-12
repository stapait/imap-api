//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2/imapclient"

	"imap-api/internal/imapx"
)

// TestGetFolder_Success cobre o caso feliz: pedir uma pasta específica
// deve devolver só ela (não as outras pastas da conta), com o mesmo
// shape de GET /folders.
func TestGetFolder_Success(t *testing.T) {
	resp := doFolderRequest(t, "INBOX", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	var body foldersResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}

	if len(body.Folders) != 1 {
		t.Fatalf("esperava só a INBOX na resposta, recebeu: %+v", body.Folders)
	}
	if body.Folders[0].Name != "INBOX" {
		t.Errorf("name = %q, esperado %q", body.Folders[0].Name, "INBOX")
	}
	// docker/emails/INBOX/*.eml é populado com vários fixtures — a
	// contagem deve refletir isso (não checamos o número exato pra não
	// acoplar o teste à quantidade de arquivos ali).
	if body.Folders[0].Messages == 0 {
		t.Errorf("messages = 0, esperava mensagens (INBOX é repopulada a partir de docker/emails/INBOX)")
	}
}

// TestGetFolder_IncludesSubfolders cobre a regra de que, se a pasta
// pedida tiver sub-pastas, elas também devem vir na resposta.
func TestGetFolder_IncludesSubfolders(t *testing.T) {
	const (
		parent = "TesteIntegracao"
		child  = "TesteIntegracao/Sub"
	)
	createTestMailboxes(t, parent, child)

	resp := doFolderRequest(t, parent, testIMAPUser, testIMAPPassword)
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
	for _, expected := range []string{parent, child} {
		if !names[expected] {
			t.Errorf("pasta %q não encontrada na resposta: %+v", expected, body.Folders)
		}
	}
}

// TestGetFolder_NestedPath cobre pedir diretamente uma pasta aninhada
// pela URL. Como o delimitador hierárquico do Dovecot local é "/",
// igual ao separador de path da URL, a barra da pasta precisa vir
// percent-encoded (%2F) — doFolderRequest cuida disso.
func TestGetFolder_NestedPath(t *testing.T) {
	const (
		parent = "TesteIntegracao2"
		child  = "TesteIntegracao2/Sub"
	)
	createTestMailboxes(t, parent, child)

	resp := doFolderRequest(t, child, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	var body foldersResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}

	if len(body.Folders) != 1 || body.Folders[0].Name != child {
		t.Errorf("esperava só %q na resposta, recebeu: %+v", child, body.Folders)
	}
}

// TestGetFolder_LiteralSlashNotFound cobre o caso de a pasta ser
// passada com a barra literal (não percent-encoded) — vira mais um
// segmento de path e o próprio ServeMux devolve 404, sem nem chegar no
// handler (só %2F é aceito como delimitador de nível — ver
// api/README.md).
func TestGetFolder_LiteralSlashNotFound(t *testing.T) {
	const (
		parent = "TesteIntegracao3"
		child  = "TesteIntegracao3/Sub"
	)
	createTestMailboxes(t, parent, child)

	req, err := http.NewRequest(http.MethodGet, server.URL+"/folders/"+child, nil)
	if err != nil {
		t.Fatalf("montando requisição: %v", err)
	}
	req.Header.Set("imapUser", testIMAPUser)
	req.Header.Set("imapPassword", testIMAPPassword)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d (barra literal não deveria casar com a rota)", resp.StatusCode, http.StatusNotFound)
	}
}

// TestGetFolder_NotFound cobre pedir uma pasta que não existe.
func TestGetFolder_NotFound(t *testing.T) {
	resp := doFolderRequest(t, "PastaQueNaoExiste", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestGetFolder_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword — deve ser rejeitada com 401, sem nem tentar
// falar com o IMAP.
func TestGetFolder_MissingHeaders(t *testing.T) {
	resp, err := http.Get(server.URL + "/folders/INBOX")
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestGetFolder_InvalidCredentials cobre credenciais IMAP inválidas —
// a autenticação no Dovecot deve falhar e a API deve reportar 401.
func TestGetFolder_InvalidCredentials(t *testing.T) {
	resp := doFolderRequest(t, "INBOX", testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestDeleteFolder_Success cobre apagar uma pasta sem sub-pastas: ela
// deixa de existir depois (a própria API confirma via GET, que passa a
// dar 404). Cria a pasta direto por IMAP, sem createTestMailboxes —
// esse teste já apaga a pasta pelo próprio endpoint, então o
// t.Cleanup de createTestMailboxes (que tentaria apagar de novo)
// daria erro.
func TestDeleteFolder_Success(t *testing.T) {
	const folder = "TesteApagarPasta"

	err := imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		return c.Create(folder, nil).Wait()
	})
	if err != nil {
		t.Fatalf("criando pasta de teste %q: %v", folder, err)
	}

	resp := doDeleteFolderRequest(t, folder, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}

	get := doFolderRequest(t, folder, testIMAPUser, testIMAPPassword)
	defer get.Body.Close()
	if get.StatusCode != http.StatusNotFound {
		t.Errorf("GET /folders/%s depois de apagar: status = %d, esperado %d (pasta deveria ter sumido)", folder, get.StatusCode, http.StatusNotFound)
	}
}

// TestDeleteFolder_Recursive cobre apagar uma pasta com sub-pastas:
// tanto ela quanto as sub-pastas devem sumir, mesmo o servidor
// recusando apagar uma pasta que ainda tem filhas (por isso a ordem
// de apagar de handleDeleteFolder é da mais aninhada pra pasta pedida
// por último).
func TestDeleteFolder_Recursive(t *testing.T) {
	const (
		parent = "TesteApagarPastaRecursivo"
		child  = "TesteApagarPastaRecursivo/Sub"
	)

	err := imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		if err := c.Create(parent, nil).Wait(); err != nil {
			return err
		}
		return c.Create(child, nil).Wait()
	})
	if err != nil {
		t.Fatalf("criando pastas de teste: %v", err)
	}

	resp := doDeleteFolderRequest(t, parent, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}

	err = imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		mailboxes, err := c.List("", parent+"*", nil).Collect()
		if err != nil {
			return err
		}
		if len(mailboxes) != 0 {
			t.Errorf("pastas ainda existem depois de apagar: %+v", mailboxes)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verificando pastas remanescentes: %v", err)
	}
}

// TestDeleteFolder_NotFound cobre apagar uma pasta que não existe.
func TestDeleteFolder_NotFound(t *testing.T) {
	resp := doDeleteFolderRequest(t, "PastaQueNaoExiste", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestDeleteFolder_InboxForbidden cobre que a INBOX nunca pode ser
// apagada — regra do próprio protocolo IMAP (RFC 3501), validada pela
// API antes de tentar no servidor.
func TestDeleteFolder_InboxForbidden(t *testing.T) {
	resp := doDeleteFolderRequest(t, "INBOX", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestDeleteFolder_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword.
func TestDeleteFolder_MissingHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodDelete, server.URL+"/folders/INBOX", nil)
	if err != nil {
		t.Fatalf("montando requisição: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestDeleteFolder_InvalidCredentials cobre credenciais IMAP inválidas
// — checadas antes da regra de negócio da INBOX, então usar "INBOX"
// aqui ainda exercita o caminho de autenticação.
func TestDeleteFolder_InvalidCredentials(t *testing.T) {
	resp := doDeleteFolderRequest(t, "INBOX", testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestCreateFolder_Success cobre o caso feliz: a pasta passa a existir
// depois (confirmado via GET), com o body devolvido já refletindo o
// nome pedido.
func TestCreateFolder_Success(t *testing.T) {
	const folder = "TesteCriarPasta"
	t.Cleanup(func() { deleteTestMailboxIfExists(t, folder) })

	resp := doCreateFolderRequest(t, folder, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusCreated)
	}

	var body folderInfo
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}
	if body.Name != folder {
		t.Errorf("name = %q, esperado %q", body.Name, folder)
	}
	if body.Messages != 0 || body.Unread != 0 {
		t.Errorf("messages/unread = %d/%d, esperado 0/0 (pasta recém-criada)", body.Messages, body.Unread)
	}

	get := doFolderRequest(t, folder, testIMAPUser, testIMAPPassword)
	defer get.Body.Close()
	if get.StatusCode != http.StatusOK {
		t.Errorf("GET /folders/%s depois de criar: status = %d, esperado %d", folder, get.StatusCode, http.StatusOK)
	}
}

// TestCreateFolder_NestedPath cobre criar diretamente uma sub-pasta
// (barra percent-encoded, %2F) — a pasta-pai (docker/emails não tem
// "TesteCriarPastaAninhada") não precisa existir antes: o Dovecot local
// cria a hierarquia inteira num único CREATE.
func TestCreateFolder_NestedPath(t *testing.T) {
	const (
		parent = "TesteCriarPastaAninhada"
		child  = "TesteCriarPastaAninhada/Sub"
	)
	t.Cleanup(func() { deleteTestMailboxIfExists(t, child, parent) })

	resp := doCreateFolderRequest(t, child, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusCreated)
	}

	get := doFolderRequest(t, child, testIMAPUser, testIMAPPassword)
	defer get.Body.Close()
	if get.StatusCode != http.StatusOK {
		t.Errorf("GET /folders/%s depois de criar: status = %d, esperado %d", child, get.StatusCode, http.StatusOK)
	}
}

// TestCreateFolder_AlreadyExists cobre criar uma pasta que já existe.
func TestCreateFolder_AlreadyExists(t *testing.T) {
	const folder = "TesteCriarPastaJaExiste"
	createTestMailboxes(t, folder)

	resp := doCreateFolderRequest(t, folder, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusConflict)
	}
}

// TestCreateFolder_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword.
func TestCreateFolder_MissingHeaders(t *testing.T) {
	resp, err := http.Post(server.URL+"/folders/TesteCriarPastaSemHeaders", "", nil)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestCreateFolder_InvalidCredentials cobre credenciais IMAP inválidas.
func TestCreateFolder_InvalidCredentials(t *testing.T) {
	resp := doCreateFolderRequest(t, "TesteCriarPastaCredencialInvalida", testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestRenameFolder_Success cobre o caso feliz: a pasta antiga some, a
// nova passa a existir, e o body devolvido reflete o novo nome.
func TestRenameFolder_Success(t *testing.T) {
	const (
		oldName = "TesteRenomearPastaAntiga"
		newName = "TesteRenomearPastaNova"
	)
	// Cria direto por IMAP (não createTestMailboxes): esse teste renomeia
	// a pasta pelo próprio endpoint, então oldName deixa de existir — o
	// t.Cleanup de createTestMailboxes (que tentaria apagar oldName de
	// novo) daria erro de pasta inexistente.
	err := imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		return c.Create(oldName, nil).Wait()
	})
	if err != nil {
		t.Fatalf("criando pasta de teste %q: %v", oldName, err)
	}
	t.Cleanup(func() { deleteTestMailboxIfExists(t, newName) })

	resp := doRenameFolderRequest(t, oldName, newName, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	var body foldersResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}
	if len(body.Folders) != 1 || body.Folders[0].Name != newName {
		t.Errorf("esperava só %q na resposta, recebeu: %+v", newName, body.Folders)
	}

	oldGet := doFolderRequest(t, oldName, testIMAPUser, testIMAPPassword)
	defer oldGet.Body.Close()
	if oldGet.StatusCode != http.StatusNotFound {
		t.Errorf("GET /folders/%s (nome antigo) depois de renomear: status = %d, esperado %d", oldName, oldGet.StatusCode, http.StatusNotFound)
	}

	newGet := doFolderRequest(t, newName, testIMAPUser, testIMAPPassword)
	defer newGet.Body.Close()
	if newGet.StatusCode != http.StatusOK {
		t.Errorf("GET /folders/%s (nome novo) depois de renomear: status = %d, esperado %d", newName, newGet.StatusCode, http.StatusOK)
	}
}

// TestRenameFolder_NotFound cobre renomear uma pasta que não existe.
func TestRenameFolder_NotFound(t *testing.T) {
	resp := doRenameFolderRequest(t, "PastaQueNaoExiste", "NovoNome", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestRenameFolder_AlreadyExists cobre renomear pra um nome que já é
// usado por outra pasta.
func TestRenameFolder_AlreadyExists(t *testing.T) {
	const (
		folderA = "TesteRenomearConflitoA"
		folderB = "TesteRenomearConflitoB"
	)
	createTestMailboxes(t, folderA, folderB)

	resp := doRenameFolderRequest(t, folderA, folderB, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusConflict)
	}
}

// TestRenameFolder_InboxForbidden cobre que a INBOX nunca pode ser
// renomeada.
func TestRenameFolder_InboxForbidden(t *testing.T) {
	resp := doRenameFolderRequest(t, "INBOX", "NovaCaixaEntrada", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestRenameFolder_MissingName cobre um body sem "name" (ou vazio).
func TestRenameFolder_MissingName(t *testing.T) {
	const folder = "TesteRenomearSemNome"
	createTestMailboxes(t, folder)

	resp := doRenameFolderRequest(t, folder, "", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestRenameFolder_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword.
func TestRenameFolder_MissingHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPatch, server.URL+"/folders/INBOX", strings.NewReader(`{"name":"Novo"}`))
	if err != nil {
		t.Fatalf("montando requisição: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestRenameFolder_InvalidCredentials cobre credenciais IMAP inválidas.
func TestRenameFolder_InvalidCredentials(t *testing.T) {
	resp := doRenameFolderRequest(t, "INBOX", "Novo", testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// doCreateFolderRequest monta a requisição pra POST /folders/{pasta}.
func doCreateFolderRequest(t *testing.T, folder, user, password string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/folders/"+url.PathEscape(folder), nil)
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

// doRenameFolderRequest monta a requisição pra PATCH /folders/{pasta},
// com body {"name": newName} — newName vazio manda um body sem "name"
// (pra cobrir a validação).
func doRenameFolderRequest(t *testing.T, folder, newName, user, password string) *http.Response {
	t.Helper()

	body := "{}"
	if newName != "" {
		data, err := json.Marshal(map[string]string{"name": newName})
		if err != nil {
			t.Fatalf("montando body: %v", err)
		}
		body = string(data)
	}

	req, err := http.NewRequest(http.MethodPatch, server.URL+"/folders/"+url.PathEscape(folder), strings.NewReader(body))
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

// deleteTestMailboxIfExists apaga as pastas informadas (ordem dada),
// ignorando erro de pasta inexistente — usado em t.Cleanup de testes
// que só criam a pasta indiretamente (via POST /folders/{pasta}) ou que
// podem ter falhado antes de chegar a criá-la.
func deleteTestMailboxIfExists(t *testing.T, mailboxes ...string) {
	t.Helper()

	_ = imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		for _, mbox := range mailboxes {
			_ = c.Delete(mbox).Wait()
		}
		return nil
	})
}

// doDeleteFolderRequest monta a requisição pra DELETE /folders/{pasta}.
func doDeleteFolderRequest(t *testing.T, folder, user, password string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodDelete, server.URL+"/folders/"+url.PathEscape(folder), nil)
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

// doFolderRequest monta a requisição pra GET /folders/{pasta},
// percent-encoding o nome da pasta — inclusive a barra de nível
// hierárquico (%2F), já que a API só aceita esse formato (ver
// api/README.md).
func doFolderRequest(t *testing.T, folder, user, password string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/folders/"+url.PathEscape(folder), nil)
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

// createTestMailboxes cria as pastas informadas (na ordem dada — pai
// antes do filho) direto via IMAP, e registra a limpeza (ordem
// inversa) no final do teste via t.Cleanup.
func createTestMailboxes(t *testing.T, mailboxes ...string) {
	t.Helper()

	err := imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		for _, mbox := range mailboxes {
			if err := c.Create(mbox, nil).Wait(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("criando pastas de teste %v: %v", mailboxes, err)
	}

	t.Cleanup(func() {
		err := imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
			for i := len(mailboxes) - 1; i >= 0; i-- {
				if err := c.Delete(mailboxes[i]).Wait(); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Errorf("limpando pastas de teste %v: %v", mailboxes, err)
		}
	})
}
