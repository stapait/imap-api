//go:build integration

package integration

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"testing"
	"time"
)

// TestGetAttachment_Success cobre o caso feliz: o attachmentLink
// devolvido por GET .../emails/{uid} baixa o conteúdo binário certo, com
// Content-Type e Content-Disposition (nome do arquivo) corretos.
func TestGetAttachment_Success(t *testing.T) {
	const folder = "TesteAnexoDownload"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, richMessage(
		`"Fulano de Tal" <fulano@example.com>`,
		"destino@example.com",
		"copia@example.com",
		"Assunto com anexo",
		time.Now(),
	))

	link := attachmentLinkFromEmail(t, folder, uid, 0)

	resp := doAttachmentLinkRequest(t, link, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("lendo corpo: %v", err)
	}
	if string(body) != "conteudo do anexo" {
		t.Errorf("corpo = %q, esperado %q", string(body), "conteudo do anexo")
	}

	if ct := resp.Header.Get("Content-Type"); ct != "text/plain" {
		t.Errorf("Content-Type = %q, esperado %q", ct, "text/plain")
	}

	_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("parseando Content-Disposition (%q): %v", resp.Header.Get("Content-Disposition"), err)
	}
	if params["filename"] != "anexo.txt" {
		t.Errorf("filename do Content-Disposition = %q, esperado %q", params["filename"], "anexo.txt")
	}
}

// TestGetAttachment_DoesNotMarkAsRead cobre que, diferente de GET
// .../emails/{uid}, baixar um anexo não marca a mensagem como lida —
// usa um attachmentId montado à mão (sem passar por GET .../emails/{uid}
// antes, que marcaria \Seen por conta própria) pra isolar o efeito
// colateral do download em si.
func TestGetAttachment_DoesNotMarkAsRead(t *testing.T) {
	const folder = "TesteAnexoNaoMarcaLida"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, attachmentMessage("remetente@example.com", "Anexo sem marcar lida", time.Now()))

	link := fmt.Sprintf("/folders/%s/emails/%d/attachments/%s", folder, uid, testAttachmentID(0, "anexo.txt"))
	resp := doAttachmentLinkRequest(t, link, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}
	if isSeenOnServer(t, folder, uid) {
		t.Error("mensagem está \\Seen no servidor depois de só baixar um anexo — não deveria marcar como lida")
	}
}

// TestGetAttachment_IndexOutOfRange cobre um attachmentId sintaticamente
// válido (base64 de "índice:nome"), mas cujo índice não existe na
// mensagem (ela tem menos anexos que isso).
func TestGetAttachment_IndexOutOfRange(t *testing.T) {
	const folder = "TesteAnexoIndiceForaDoAlcance"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, plainTextMessage("remetente@example.com", "Sem anexo", time.Now()))

	link := fmt.Sprintf("/folders/%s/emails/%d/attachments/%s", folder, uid, testAttachmentID(0, "nao-existe.txt"))
	resp := doAttachmentLinkRequest(t, link, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestGetAttachment_InvalidID cobre um attachmentId que não decodifica
// pro formato "índice:nome" (não veio de attachmentLink).
func TestGetAttachment_InvalidID(t *testing.T) {
	link := "/folders/INBOX/emails/1/attachments/isto-nao-e-base64-valido!!!"
	resp := doAttachmentLinkRequest(t, link, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestGetAttachment_EmailNotFound cobre uma pasta existente mas um uid
// que não existe.
func TestGetAttachment_EmailNotFound(t *testing.T) {
	link := fmt.Sprintf("/folders/INBOX/emails/999999999/attachments/%s", testAttachmentID(0, "x"))
	resp := doAttachmentLinkRequest(t, link, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestGetAttachment_FolderNotFound cobre uma pasta que não existe.
func TestGetAttachment_FolderNotFound(t *testing.T) {
	link := fmt.Sprintf("/folders/PastaQueNaoExiste/emails/1/attachments/%s", testAttachmentID(0, "x"))
	resp := doAttachmentLinkRequest(t, link, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestGetAttachment_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword.
func TestGetAttachment_MissingHeaders(t *testing.T) {
	link := fmt.Sprintf("/folders/INBOX/emails/1/attachments/%s", testAttachmentID(0, "x"))
	resp, err := http.Get(server.URL + link)
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestGetAttachment_InvalidCredentials cobre credenciais IMAP inválidas.
func TestGetAttachment_InvalidCredentials(t *testing.T) {
	link := fmt.Sprintf("/folders/INBOX/emails/1/attachments/%s", testAttachmentID(0, "x"))
	resp := doAttachmentLinkRequest(t, link, testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestGetAttachment_SmokeAllMessages é o teste de fumaça deste endpoint
// (ver smoke_test.go): pra cada mensagem real da caixa de teste, busca
// o e-mail completo (pra obter attachmentLink de cada anexo, se houver)
// e garante que baixar qualquer um deles nunca derruba o endpoint com
// 5xx — mesmo propósito de TestGetEmail_SmokeAllMessages, mas validando
// o parsing usado por findAttachment (attachments.go) contra a mesma
// diversidade/malformação real dos fixtures.
func TestGetAttachment_SmokeAllMessages(t *testing.T) {
	refs := allFolderEmailRefs(t)
	if len(refs) == 0 {
		t.Fatal("nenhuma mensagem encontrada na caixa de teste — populate_mailbox.py rodou?")
	}

	runSmokeTest(t, refs, func(t *testing.T, ref folderEmailRef) {
		emailResp := doEmailRequest(t, ref.Folder, ref.UID, testIMAPUser, testIMAPPassword)
		defer emailResp.Body.Close()

		if emailResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(emailResp.Body)
			t.Errorf("GET email: status = %d (corpo: %s)", emailResp.StatusCode, body)
			return
		}

		var detail emailDetail
		if err := json.NewDecoder(emailResp.Body).Decode(&detail); err != nil {
			t.Errorf("decodificando e-mail: %v", err)
			return
		}

		for _, att := range detail.Attachments {
			attResp := doAttachmentLinkRequest(t, att.AttachmentLink, testIMAPUser, testIMAPPassword)
			if attResp.StatusCode/100 == 5 {
				body, _ := io.ReadAll(attResp.Body)
				t.Errorf("GET %s: status = %d (corpo: %s)", att.AttachmentLink, attResp.StatusCode, body)
			}
			attResp.Body.Close()
		}
	})
}

// attachmentLinkFromEmail busca GET .../emails/{uid} e devolve o
// attachmentLink do anexo de índice index.
func attachmentLinkFromEmail(t *testing.T, folder string, uid uint32, index int) string {
	t.Helper()

	resp := doEmailRequest(t, folder, uid, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	var detail emailDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatalf("decodificando e-mail: %v", err)
	}
	if index >= len(detail.Attachments) {
		t.Fatalf("mensagem uid=%d em %s tem %d anexo(s), pedido índice %d", uid, folder, len(detail.Attachments), index)
	}
	return detail.Attachments[index].AttachmentLink
}

// doAttachmentLinkRequest monta a requisição pro path relativo link
// (ex: o valor de attachmentLink, ou um montado à mão pelos testes de
// erro).
func doAttachmentLinkRequest(t *testing.T, link, user, password string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, server.URL+link, nil)
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

// testAttachmentID replica o formato de internal/httpapi/attachments.go
// (attachmentID: base64 URL-safe sem padding de "índice:nome") — usado
// pelos testes de erro, que precisam montar um attachmentId sem antes
// chamar GET .../emails/{uid} (que marcaria a mensagem como lida).
func testAttachmentID(index int, filename string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%s", index, filename)))
}
