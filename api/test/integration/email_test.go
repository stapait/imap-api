//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"imap-api/internal/imapx"
)

type emailAddress struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type attachmentInfo struct {
	Filename       string `json:"filename"`
	ContentType    string `json:"contentType"`
	Size           int64  `json:"size"`
	AttachmentLink string `json:"attachmentLink"`
}

type emailDetail struct {
	UID         uint32           `json:"uid"`
	Subject     string           `json:"subject"`
	From        emailAddress     `json:"from"`
	To          []emailAddress   `json:"to"`
	Cc          []emailAddress   `json:"cc"`
	Date        time.Time        `json:"date"`
	Read        bool             `json:"read"`
	Starred     bool             `json:"starred"`
	Answered    bool             `json:"answered"`
	Forwarded   bool             `json:"forwarded"`
	MimeType    string           `json:"mimeType"`
	Body        string           `json:"body"`
	Attachments []attachmentInfo `json:"attachments"`
}

// TestGetEmail_Success cobre o caso feliz: metadados (assunto,
// remetente/destinatários, data, flags), corpo (HTML tem preferência
// sobre texto simples quando a mensagem tem os dois) e a lista leve de
// anexos (sem conteúdo binário) de uma mensagem com estrutura rica
// (multipart/alternative dentro de multipart/mixed com anexo).
func TestGetEmail_Success(t *testing.T) {
	const folder = "TesteEmailDetalhe"
	createTestMailboxes(t, folder)

	date := time.Date(2024, 3, 10, 8, 0, 0, 0, time.UTC)
	uid := appendTestMessageUID(t, folder, richMessage(
		`"Fulano de Tal" <fulano@example.com>`,
		"destino@example.com",
		"copia@example.com",
		"Assunto rico",
		date,
	), imap.FlagAnswered)

	resp := doEmailRequest(t, folder, uid, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	var body emailDetail
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}

	if body.UID != uid {
		t.Errorf("uid = %d, esperado %d", body.UID, uid)
	}
	if body.Subject != "Assunto rico" {
		t.Errorf("subject = %q, esperado %q", body.Subject, "Assunto rico")
	}
	if body.From.Name != "Fulano de Tal" || body.From.Email != "fulano@example.com" {
		t.Errorf("from = %+v, esperado {Fulano de Tal fulano@example.com}", body.From)
	}
	if len(body.To) != 1 || body.To[0].Email != "destino@example.com" {
		t.Errorf("to = %+v, esperado [{.. destino@example.com}]", body.To)
	}
	if len(body.Cc) != 1 || body.Cc[0].Email != "copia@example.com" {
		t.Errorf("cc = %+v, esperado [{.. copia@example.com}]", body.Cc)
	}
	if !body.Date.Equal(date) {
		t.Errorf("date = %v, esperado %v", body.Date, date)
	}
	if !body.Answered {
		t.Error("answered = false, esperado true (mensagem entregue com a flag \\Answered)")
	}
	if body.Starred {
		t.Error("starred = true, esperado false (mensagem sem a flag \\Flagged)")
	}
	if body.Forwarded {
		t.Error("forwarded = true, esperado false (mensagem sem a flag $Forwarded)")
	}

	if body.MimeType != "text/html" {
		t.Errorf("mimeType = %q, esperado %q (HTML tem preferência sobre texto simples)", body.MimeType, "text/html")
	}
	if !strings.Contains(body.Body, "<b>rico</b>") {
		t.Errorf("body = %q, esperado que contivesse %q", body.Body, "<b>rico</b>")
	}

	if len(body.Attachments) != 1 {
		t.Fatalf("esperava 1 anexo, recebeu %d: %+v", len(body.Attachments), body.Attachments)
	}
	att := body.Attachments[0]
	if att.Filename != "anexo.txt" {
		t.Errorf("attachments[0].filename = %q, esperado %q", att.Filename, "anexo.txt")
	}
	// O CRLF imediatamente antes do delimitador de boundary pertence ao
	// próprio delimitador (RFC 2046), não ao conteúdo da parte — por
	// isso o tamanho decodificado não inclui o "\r\n" final escrito em
	// richMessage.
	if att.Size != int64(len("conteudo do anexo")) {
		t.Errorf("attachments[0].size = %d, esperado %d", att.Size, len("conteudo do anexo"))
	}
}

// TestGetEmail_MarksAsRead cobre a marcação de \Seen: diferente da
// listagem (GET /folders/{pasta}/emails, que usa EXAMINE e nunca
// marca), buscar o e-mail completo marca a mensagem como lida — como
// um cliente de e-mail faria ao abrir uma mensagem.
func TestGetEmail_MarksAsRead(t *testing.T) {
	const folder = "TesteEmailLida"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, plainTextMessage(
		"remetente@example.com", "Mensagem não lida", time.Now(),
	))

	resp := doEmailRequest(t, folder, uid, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	var body emailDetail
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}
	if !body.Read {
		t.Error("read = false na própria resposta, esperado true (o FETCH que busca o corpo já marca \\Seen)")
	}

	if !isSeenOnServer(t, folder, uid) {
		t.Error("mensagem não está \\Seen no servidor após GET /folders/{pasta}/emails/{uid}")
	}
}

// TestGetEmail_PlainTextOnly cobre uma mensagem sem parte HTML: o
// corpo devolvido deve ser o texto simples, com mimeType
// correspondente.
func TestGetEmail_PlainTextOnly(t *testing.T) {
	const folder = "TesteEmailTextoSimples"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, plainTextMessage(
		"remetente@example.com", "Só texto", time.Now(),
	))

	resp := doEmailRequest(t, folder, uid, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	var body emailDetail
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}

	if body.MimeType != "text/plain" {
		t.Errorf("mimeType = %q, esperado %q", body.MimeType, "text/plain")
	}
	if !strings.Contains(body.Body, "Corpo de teste.") {
		t.Errorf("body = %q, esperado que contivesse %q", body.Body, "Corpo de teste.")
	}
	if len(body.Attachments) != 0 {
		t.Errorf("attachments = %+v, esperado vazio", body.Attachments)
	}
}

// TestGetEmail_UnknownTransferEncoding cobre uma mensagem com
// Content-Transfer-Encoding não reconhecido (ex: "as-is", visto em
// e-mails reais do corpus do SpamAssassin) — deve degradar (200, sem
// corpo/mimeType pra essa parte) em vez de 500. go-message/mail tem
// uma limitação onde, nesse caso, NextPart devolve só o erro sem a
// Part (apesar do que a doc promete) — o handler precisa tratar isso
// sem tentar desreferenciar uma Part nula.
func TestGetEmail_UnknownTransferEncoding(t *testing.T) {
	const folder = "TesteEmailCTEDesconhecido"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, unknownTransferEncodingMessage(
		"remetente@example.com", "CTE desconhecido", time.Now(),
	))

	resp := doEmailRequest(t, folder, uid, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d (CTE desconhecido não deveria causar erro 500)", resp.StatusCode, http.StatusOK)
	}

	var body emailDetail
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}
	if body.Subject != "CTE desconhecido" {
		t.Errorf("subject = %q, esperado %q (metadados devem vir mesmo com o corpo indisponível)", body.Subject, "CTE desconhecido")
	}
}

// TestGetEmail_SmokeAllMessages é o teste de fumaça deste endpoint
// (ver smoke_test.go): garante que nenhuma mensagem real da caixa de
// teste — todo o conteúdo de docker/emails/ e docker/emails-
// permanent/, incluindo o corpus real do SpamAssassin — derruba
// GET /folders/{pasta}/emails/{uid} com 5xx. Foi assim que o bug do
// Content-Transfer-Encoding "as-is" (ver
// TestGetEmail_UnknownTransferEncoding) deveria ter sido pego: ele
// existia em fixtures reais do corpus muito antes de virar um caso
// sintético dedicado.
func TestGetEmail_SmokeAllMessages(t *testing.T) {
	refs := allFolderEmailRefs(t)
	if len(refs) == 0 {
		t.Fatal("nenhuma mensagem encontrada na caixa de teste — populate_mailbox.py rodou?")
	}

	runSmokeTest(t, refs, func(t *testing.T, ref folderEmailRef) {
		resp := doEmailRequest(t, ref.Folder, ref.UID, testIMAPUser, testIMAPPassword)
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Errorf("status = %d, esperado %d (corpo: %s)", resp.StatusCode, http.StatusOK, body)
		}
	})
}

// TestGetEmail_NotFound cobre um UID que não existe na pasta — a
// pasta em si existe, só a mensagem que não.
func TestGetEmail_NotFound(t *testing.T) {
	resp := doEmailRequest(t, "INBOX", 999999999, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestGetEmail_FolderNotFound cobre uma pasta que não existe.
func TestGetEmail_FolderNotFound(t *testing.T) {
	resp := doEmailRequest(t, "PastaQueNaoExiste", 1, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestGetEmail_InvalidUID cobre um {uid} não numérico na URL.
func TestGetEmail_InvalidUID(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, server.URL+"/folders/INBOX/emails/abc", nil)
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

// TestGetEmail_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword.
func TestGetEmail_MissingHeaders(t *testing.T) {
	resp, err := http.Get(server.URL + "/folders/INBOX/emails/1")
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestGetEmail_InvalidCredentials cobre credenciais IMAP inválidas.
func TestGetEmail_InvalidCredentials(t *testing.T) {
	resp := doEmailRequest(t, "INBOX", 1, testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// doEmailRequest monta a requisição pra GET /folders/{pasta}/emails/{uid}.
func doEmailRequest(t *testing.T, folder string, uid uint32, user, password string) *http.Response {
	t.Helper()

	url := fmt.Sprintf("%s/folders/%s/emails/%d", server.URL, folder, uid)
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

// appendTestMessageUID entrega msg em mailbox via IMAP APPEND (como
// appendTestMessage) e devolve o UID atribuído pelo servidor.
func appendTestMessageUID(t *testing.T, mailbox string, msg []byte, flags ...imap.Flag) uint32 {
	t.Helper()

	var uid uint32
	err := imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		cmd := c.Append(mailbox, int64(len(msg)), &imap.AppendOptions{Flags: flags})
		if _, err := cmd.Write(msg); err != nil {
			return err
		}
		if err := cmd.Close(); err != nil {
			return err
		}
		data, err := cmd.Wait()
		if err != nil {
			return err
		}
		if data != nil {
			uid = uint32(data.UID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("append de mensagem de teste em %s: %v", mailbox, err)
	}
	if uid != 0 {
		return uid
	}

	// Nem todo servidor devolve UIDAPPEND (RFC 4315) na resposta do
	// APPEND — nesse caso, busca o UID da mensagem recém-criada via
	// SEARCH pelo Subject, que appendTestMessage/richMessage sempre
	// preenche.
	subject := extractSubject(t, msg)
	err = imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		if _, err := c.Select(mailbox, nil).Wait(); err != nil {
			return err
		}
		data, err := c.UIDSearch(&imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: subject}},
		}, nil).Wait()
		if err != nil {
			return err
		}
		uids := data.AllUIDs()
		if len(uids) == 0 {
			return fmt.Errorf("nenhuma mensagem encontrada com Subject %q", subject)
		}
		uid = uint32(uids[len(uids)-1])
		return nil
	})
	if err != nil {
		t.Fatalf("resolvendo UID da mensagem de teste em %s: %v", mailbox, err)
	}
	return uid
}

// isSeenOnServer checa direto no IMAP (sem passar pelo endpoint) se a
// mensagem uid da pasta folder tem a flag \Seen.
func isSeenOnServer(t *testing.T, folder string, uid uint32) bool {
	t.Helper()
	return hasFlagOnServer(t, folder, uid, imap.FlagSeen)
}

// isFlaggedOnServer checa direto no IMAP se a mensagem uid da pasta
// folder tem a flag \Flagged (favoritada).
func isFlaggedOnServer(t *testing.T, folder string, uid uint32) bool {
	t.Helper()
	return hasFlagOnServer(t, folder, uid, imap.FlagFlagged)
}

// hasFlagOnServer checa direto no IMAP (sem passar pelo endpoint) se a
// mensagem uid da pasta folder tem a flag flag.
func hasFlagOnServer(t *testing.T, folder string, uid uint32, flag imap.Flag) bool {
	t.Helper()

	found := false
	err := imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		if _, err := c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
			return err
		}
		var uidSet imap.UIDSet
		uidSet.AddNum(imap.UID(uid))
		messages, err := c.Fetch(uidSet, &imap.FetchOptions{Flags: true}).Collect()
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			return fmt.Errorf("mensagem uid=%d não encontrada em %s", uid, folder)
		}
		for _, f := range messages[0].Flags {
			if f == flag {
				found = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("checando flag %s: %v", flag, err)
	}
	return found
}

// extractSubject extrai o valor do header Subject de um .eml
// montado pelos helpers deste pacote (plainTextMessage/richMessage) —
// só pra resolver o UID pós-APPEND quando o servidor não devolve
// UIDAPPEND.
func extractSubject(t *testing.T, msg []byte) string {
	t.Helper()

	for _, line := range strings.Split(string(msg), "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			return strings.TrimPrefix(line, "Subject: ")
		}
	}
	t.Fatalf("mensagem de teste sem header Subject: %q", msg)
	return ""
}

// richMessage monta um .eml multipart/mixed com um corpo
// multipart/alternative (texto simples + HTML) e um anexo — usado pra
// cobrir a preferência por HTML e a lista de anexos de
// GET /folders/{pasta}/emails/{uid}.
func richMessage(from, to, cc, subject string, date time.Time) []byte {
	const (
		mixedBoundary = "TESTE-BOUNDARY-MIXED"
		altBoundary   = "TESTE-BOUNDARY-ALT"
	)
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Cc: %s\r\n", cc)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", date.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n", mixedBoundary)
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "--%s\r\n", mixedBoundary)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n", altBoundary)
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "--%s\r\n", altBoundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString("Corpo em texto simples.\r\n")
	fmt.Fprintf(&b, "--%s\r\n", altBoundary)
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString("<p>Corpo em <b>rico</b> HTML.</p>\r\n")
	fmt.Fprintf(&b, "--%s--\r\n", altBoundary)

	fmt.Fprintf(&b, "--%s\r\n", mixedBoundary)
	b.WriteString("Content-Type: text/plain; name=\"anexo.txt\"\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"anexo.txt\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	b.WriteString("\r\n")
	b.WriteString("conteudo do anexo\r\n")
	fmt.Fprintf(&b, "--%s--\r\n", mixedBoundary)

	return []byte(b.String())
}

// unknownTransferEncodingMessage monta um .eml multipart/mixed cuja
// única parte tem um Content-Transfer-Encoding não reconhecido pelo
// RFC 2045 (ex: "as-is", visto em e-mails reais do corpus do
// SpamAssassin) — usado pra cobrir que isso não derruba
// GET /folders/{pasta}/emails/{uid} com 500.
func unknownTransferEncodingMessage(from, subject string, date time.Time) []byte {
	const boundary = "TESTE-BOUNDARY-CTE-DESCONHECIDO"
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: destino@example.com\r\n")
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", date.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary)
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Transfer-Encoding: as-is\r\n")
	b.WriteString("Content-Type: text/html; charset=ISO-8859-1\r\n")
	b.WriteString("\r\n")
	b.WriteString("<p>Corpo com CTE desconhecido.</p>\r\n")
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return []byte(b.String())
}
