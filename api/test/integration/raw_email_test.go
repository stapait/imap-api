//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"testing"
	"time"
)

// TestGetRawEmail_Success cobre o caso feliz: o corpo devolvido é
// exatamente os bytes originais da mensagem (sem nenhum parsing), com
// Content-Type/Content-Disposition corretos.
func TestGetRawEmail_Success(t *testing.T) {
	const folder = "TesteRawEmail"
	createTestMailboxes(t, folder)

	msg := plainTextMessage("remetente@example.com", "Mensagem crua", time.Now())
	uid := appendTestMessageUID(t, folder, msg)

	resp := doRawEmailRequest(t, folder, uid, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("lendo corpo: %v", err)
	}
	if !bytes.Equal(body, msg) {
		t.Errorf("corpo devolvido difere do .eml original\ngot:  %q\nwant: %q", body, msg)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "message/rfc822" {
		t.Errorf("Content-Type = %q, esperado %q", ct, "message/rfc822")
	}

	_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("parseando Content-Disposition (%q): %v", resp.Header.Get("Content-Disposition"), err)
	}
	wantFilename := fmt.Sprintf("%d.eml", uid)
	if params["filename"] != wantFilename {
		t.Errorf("filename do Content-Disposition = %q, esperado %q", params["filename"], wantFilename)
	}
}

// TestGetRawEmail_DoesNotMarkAsRead cobre que, diferente de GET
// .../emails/{uid}, baixar o raw não marca a mensagem como lida.
func TestGetRawEmail_DoesNotMarkAsRead(t *testing.T) {
	const folder = "TesteRawEmailNaoMarcaLida"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, plainTextMessage("remetente@example.com", "Raw sem marcar lida", time.Now()))

	resp := doRawEmailRequest(t, folder, uid, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}
	if isSeenOnServer(t, folder, uid) {
		t.Error("mensagem está \\Seen no servidor depois de só baixar o raw — não deveria marcar como lida")
	}
}

// TestGetRawEmail_SmokeAllMessages é o teste de fumaça deste endpoint
// (ver smoke_test.go): garante que nenhuma mensagem real da caixa de
// teste derruba GET .../emails/{uid}/raw com 5xx.
func TestGetRawEmail_SmokeAllMessages(t *testing.T) {
	refs := allFolderEmailRefs(t)
	if len(refs) == 0 {
		t.Fatal("nenhuma mensagem encontrada na caixa de teste — populate_mailbox.py rodou?")
	}

	runSmokeTest(t, refs, func(t *testing.T, ref folderEmailRef) {
		resp := doRawEmailRequest(t, ref.Folder, ref.UID, testIMAPUser, testIMAPPassword)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Errorf("status = %d, esperado %d (corpo: %s)", resp.StatusCode, http.StatusOK, body)
		}
	})
}

// TestGetRawEmail_NotFound cobre um UID que não existe na pasta.
func TestGetRawEmail_NotFound(t *testing.T) {
	resp := doRawEmailRequest(t, "INBOX", 999999999, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestGetRawEmail_FolderNotFound cobre uma pasta que não existe.
func TestGetRawEmail_FolderNotFound(t *testing.T) {
	resp := doRawEmailRequest(t, "PastaQueNaoExiste", 1, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestGetRawEmail_InvalidUID cobre um {uid} não numérico na URL.
func TestGetRawEmail_InvalidUID(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, server.URL+"/folders/INBOX/emails/abc/raw", nil)
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

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestGetRawEmail_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword.
func TestGetRawEmail_MissingHeaders(t *testing.T) {
	resp, err := http.Get(server.URL + "/folders/INBOX/emails/1/raw")
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestGetRawEmail_InvalidCredentials cobre credenciais IMAP inválidas.
func TestGetRawEmail_InvalidCredentials(t *testing.T) {
	resp := doRawEmailRequest(t, "INBOX", 1, testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// doRawEmailRequest monta a requisição pra GET
// /folders/{pasta}/emails/{uid}/raw.
func doRawEmailRequest(t *testing.T, folder string, uid uint32, user, password string) *http.Response {
	t.Helper()

	url := fmt.Sprintf("%s/folders/%s/emails/%d/raw", server.URL, folder, uid)
	req, err := http.NewRequest(http.MethodGet, url, nil)
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
