//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"imap-api/internal/imapx"
)

type searchEmailInfo struct {
	UID           uint32 `json:"uid"`
	Subject       string `json:"subject"`
	From          string `json:"from"`
	HasAttachment bool   `json:"hasAttachment"`
	Forwarded     bool   `json:"forwarded"`
	Folder        string `json:"folder"`
}

type searchResponse struct {
	Emails     []searchEmailInfo `json:"emails"`
	Page       int               `json:"page"`
	PageSize   int               `json:"pageSize"`
	Total      int               `json:"total"`
	TotalPages int               `json:"totalPages"`
}

// zzBuscaMarker é um token improvável de aparecer nos fixtures reais
// (docker/emails/, corpus do SpamAssassin etc.) — usado no assunto das
// mensagens de teste deste arquivo em conjunto com outro filtro, pra
// escopar buscas em todas as pastas (folder omitido) só às mensagens
// deste teste, mesmo com todo o resto da caixa de teste populado.
const zzBuscaMarker = "ZZBUSCATESTE"

// TestSearchEmails_BySubject cobre busca por assunto (substring) numa
// pasta específica.
func TestSearchEmails_BySubject(t *testing.T) {
	const folder = "TesteBuscaAssunto"
	createTestMailboxes(t, folder)

	appendTestMessage(t, folder, plainTextMessage("financeiro@example.com", "Relatório Mensal", time.Date(2023, 1, 10, 9, 0, 0, 0, time.UTC)))
	appendTestMessage(t, folder, plainTextMessage("rh@example.com", "Reunião de Equipe", time.Date(2023, 2, 10, 9, 0, 0, 0, time.UTC)))

	resp := doSearchRequest(t, "folder="+folder+"&subject=Relat%C3%B3rio", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	body := decodeSearchResponse(t, resp)
	if len(body.Emails) != 1 {
		t.Fatalf("esperava 1 resultado, recebeu %d: %+v", len(body.Emails), body.Emails)
	}
	if body.Emails[0].Subject != "Relatório Mensal" {
		t.Errorf("subject = %q, esperado %q", body.Emails[0].Subject, "Relatório Mensal")
	}
	if body.Emails[0].Folder != folder {
		t.Errorf("folder = %q, esperado %q", body.Emails[0].Folder, folder)
	}
}

// TestSearchEmails_AllFolders cobre busca sem "folder": mergeia
// resultados de pastas diferentes, ordenados por data decrescente
// (padrão), cada um com a pasta correta em "folder".
func TestSearchEmails_AllFolders(t *testing.T) {
	const folderA = "TesteBuscaTodasA"
	const folderB = "TesteBuscaTodasB"
	createTestMailboxes(t, folderA, folderB)

	older := time.Date(2023, 1, 10, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2023, 9, 20, 9, 0, 0, 0, time.UTC)

	appendTestMessage(t, folderA, plainTextMessage("financeiro@example.com", zzBuscaMarker+" Relatório Mensal", older))
	appendTestMessage(t, folderB, plainTextMessage("financeiro@example.com", zzBuscaMarker+" Relatório Semanal", newer))

	resp := doSearchRequest(t, "subject="+url.QueryEscape(zzBuscaMarker), testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	body := decodeSearchResponse(t, resp)
	if len(body.Emails) != 2 {
		t.Fatalf("esperava 2 resultados (uma pasta cada), recebeu %d: %+v", len(body.Emails), body.Emails)
	}

	newest, oldest := body.Emails[0], body.Emails[1]
	if newest.Folder != folderB {
		t.Errorf("emails[0].folder = %q, esperado %q (mensagem mais recente)", newest.Folder, folderB)
	}
	if oldest.Folder != folderA {
		t.Errorf("emails[1].folder = %q, esperado %q (mensagem mais antiga)", oldest.Folder, folderA)
	}
}

// TestSearchEmails_UnreadFilter cobre o filtro unread=true/false.
func TestSearchEmails_UnreadFilter(t *testing.T) {
	const folder = "TesteBuscaNaoLida"
	createTestMailboxes(t, folder)

	appendTestMessage(t, folder, plainTextMessage("a@example.com", "Não lida", time.Now()))
	appendTestMessage(t, folder, plainTextMessage("b@example.com", "Lida", time.Now()), imap.FlagSeen)

	unread := doSearchRequest(t, "folder="+folder+"&unread=true", testIMAPUser, testIMAPPassword)
	defer unread.Body.Close()
	unreadBody := decodeSearchResponse(t, unread)
	if len(unreadBody.Emails) != 1 || unreadBody.Emails[0].Subject != "Não lida" {
		t.Errorf("unread=true: esperava só \"Não lida\", recebeu %+v", unreadBody.Emails)
	}

	read := doSearchRequest(t, "folder="+folder+"&unread=false", testIMAPUser, testIMAPPassword)
	defer read.Body.Close()
	readBody := decodeSearchResponse(t, read)
	if len(readBody.Emails) != 1 || readBody.Emails[0].Subject != "Lida" {
		t.Errorf("unread=false: esperava só \"Lida\", recebeu %+v", readBody.Emails)
	}
}

// TestSearchEmails_StarredFilter cobre o filtro starred=true/false.
func TestSearchEmails_StarredFilter(t *testing.T) {
	const folder = "TesteBuscaFavorita"
	createTestMailboxes(t, folder)

	appendTestMessage(t, folder, plainTextMessage("a@example.com", "Favorita", time.Now()), imap.FlagFlagged)
	appendTestMessage(t, folder, plainTextMessage("b@example.com", "Comum", time.Now()))

	resp := doSearchRequest(t, "folder="+folder+"&starred=true", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	body := decodeSearchResponse(t, resp)
	if len(body.Emails) != 1 || body.Emails[0].Subject != "Favorita" {
		t.Errorf("starred=true: esperava só \"Favorita\", recebeu %+v", body.Emails)
	}
}

// TestSearchEmails_HasAttachmentFilter cobre o filtro hasAttachment —
// heurística de texto (TEXT "filename="), não o BODYSTRUCTURE (ver
// comentário de searchAttachmentText em internal/httpapi/search.go).
func TestSearchEmails_HasAttachmentFilter(t *testing.T) {
	const folder = "TesteBuscaAnexo"
	createTestMailboxes(t, folder)

	appendTestMessage(t, folder, attachmentMessage("a@example.com", "Com anexo", time.Now()))
	appendTestMessage(t, folder, plainTextMessage("b@example.com", "Sem anexo", time.Now()))

	withAttachment := doSearchRequest(t, "folder="+folder+"&hasAttachment=true", testIMAPUser, testIMAPPassword)
	defer withAttachment.Body.Close()
	withBody := decodeSearchResponse(t, withAttachment)
	if len(withBody.Emails) != 1 || withBody.Emails[0].Subject != "Com anexo" {
		t.Errorf("hasAttachment=true: esperava só \"Com anexo\", recebeu %+v", withBody.Emails)
	}
	if !withBody.Emails[0].HasAttachment {
		t.Error("hasAttachment do resultado = false, esperado true (calculado via BODYSTRUCTURE)")
	}

	without := doSearchRequest(t, "folder="+folder+"&hasAttachment=false", testIMAPUser, testIMAPPassword)
	defer without.Body.Close()
	withoutBody := decodeSearchResponse(t, without)
	if len(withoutBody.Emails) != 1 || withoutBody.Emails[0].Subject != "Sem anexo" {
		t.Errorf("hasAttachment=false: esperava só \"Sem anexo\", recebeu %+v", withoutBody.Emails)
	}
}

// TestSearchEmails_SinceBefore cobre os filtros de data since/before.
// IMAP SINCE/BEFORE filtram pelo INTERNALDATE da mensagem (data de
// entrega na pasta), não pelo header Date: da mensagem em si — por isso
// usa appendTestMessageWithDate (que seta AppendOptions.Time) em vez de
// appendTestMessage (que deixa o servidor usar a hora do APPEND).
func TestSearchEmails_SinceBefore(t *testing.T) {
	const folder = "TesteBuscaData"
	createTestMailboxes(t, folder)

	appendTestMessageWithDate(t, folder, plainTextMessage("a@example.com", "Antiga", time.Date(2023, 1, 10, 9, 0, 0, 0, time.UTC)), time.Date(2023, 1, 10, 9, 0, 0, 0, time.UTC))
	appendTestMessageWithDate(t, folder, plainTextMessage("b@example.com", "No período", time.Date(2023, 6, 15, 9, 0, 0, 0, time.UTC)), time.Date(2023, 6, 15, 9, 0, 0, 0, time.UTC))
	appendTestMessageWithDate(t, folder, plainTextMessage("c@example.com", "Nova", time.Date(2023, 12, 1, 9, 0, 0, 0, time.UTC)), time.Date(2023, 12, 1, 9, 0, 0, 0, time.UTC))

	resp := doSearchRequest(t, "folder="+folder+"&since=2023-05-01&before=2023-07-01", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	body := decodeSearchResponse(t, resp)
	if len(body.Emails) != 1 || body.Emails[0].Subject != "No período" {
		t.Errorf("since/before: esperava só \"No período\", recebeu %+v", body.Emails)
	}
}

// TestSearchEmails_Pagination cobre page/pageSize sobre o resultado da
// busca — pageSize mínimo é 10 (mesma validação de
// GET /folders/{pasta}/emails), por isso 12 mensagens pra ter 2 páginas.
func TestSearchEmails_Pagination(t *testing.T) {
	const folder = "TesteBuscaPaginacao"
	const total = 12
	const pageSize = 10
	createTestMailboxes(t, folder)

	for i := 0; i < total; i++ {
		date := time.Date(2023, 1, 1+i, 9, 0, 0, 0, time.UTC)
		appendTestMessage(t, folder, plainTextMessage("a@example.com", fmt.Sprintf("Mensagem %d", i), date))
	}

	first := doSearchRequest(t, fmt.Sprintf("folder=%s&pageSize=%d&page=1", folder, pageSize), testIMAPUser, testIMAPPassword)
	defer first.Body.Close()
	firstBody := decodeSearchResponse(t, first)
	if len(firstBody.Emails) != pageSize {
		t.Fatalf("página 1: esperava %d e-mails, recebeu %d", pageSize, len(firstBody.Emails))
	}
	if firstBody.Total != total || firstBody.TotalPages != 2 {
		t.Errorf("total/totalPages = %d/%d, esperado %d/2", firstBody.Total, firstBody.TotalPages, total)
	}

	second := doSearchRequest(t, fmt.Sprintf("folder=%s&pageSize=%d&page=2", folder, pageSize), testIMAPUser, testIMAPPassword)
	defer second.Body.Close()
	secondBody := decodeSearchResponse(t, second)
	if len(secondBody.Emails) != total-pageSize {
		t.Fatalf("página 2: esperava %d e-mail(s), recebeu %d", total-pageSize, len(secondBody.Emails))
	}
}

// TestSearchEmails_NoFilters cobre uma busca sem nenhum filtro (só
// folder) — equivale a IMAP SEARCH ALL, devolve tudo da pasta.
func TestSearchEmails_NoFilters(t *testing.T) {
	const folder = "TesteBuscaSemFiltro"
	createTestMailboxes(t, folder)

	appendTestMessage(t, folder, plainTextMessage("a@example.com", "Um", time.Now()))
	appendTestMessage(t, folder, plainTextMessage("b@example.com", "Dois", time.Now()))

	resp := doSearchRequest(t, "folder="+folder, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	body := decodeSearchResponse(t, resp)
	if len(body.Emails) != 2 {
		t.Errorf("esperava 2 e-mails (sem filtro = ALL), recebeu %d: %+v", len(body.Emails), body.Emails)
	}
}

// TestSearchEmails_FolderNotFound cobre busca numa pasta específica que
// não existe.
func TestSearchEmails_FolderNotFound(t *testing.T) {
	resp := doSearchRequest(t, "folder=PastaQueNaoExiste", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestSearchEmails_InvalidParams cobre a validação dos parâmetros
// específicos de busca (hasAttachment/starred/unread/since/before) —
// todos devem dar 400 quando fora do esperado.
func TestSearchEmails_InvalidParams(t *testing.T) {
	for _, query := range []string{
		"hasAttachment=talvez",
		"starred=talvez",
		"unread=talvez",
		"since=10-01-2023",
		"before=não-é-uma-data",
	} {
		t.Run(query, func(t *testing.T) {
			resp := doSearchRequest(t, query, testIMAPUser, testIMAPPassword)
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, esperado %d (query=%q)", resp.StatusCode, http.StatusBadRequest, query)
			}
		})
	}
}

// TestSearchEmails_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword.
func TestSearchEmails_MissingHeaders(t *testing.T) {
	resp, err := http.Get(server.URL + "/emails/search")
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestSearchEmails_InvalidCredentials cobre credenciais IMAP inválidas.
func TestSearchEmails_InvalidCredentials(t *testing.T) {
	resp := doSearchRequest(t, "", testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// appendTestMessageWithDate é igual appendTestMessage, mas fixa o
// INTERNALDATE da mensagem (via imap.AppendOptions.Time) em vez de
// deixar o servidor usar a hora do APPEND — necessário pros testes de
// since/before, que filtram pelo INTERNALDATE (ver
// TestSearchEmails_SinceBefore).
func appendTestMessageWithDate(t *testing.T, mailbox string, msg []byte, date time.Time) {
	t.Helper()

	err := imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		cmd := c.Append(mailbox, int64(len(msg)), &imap.AppendOptions{Time: date})
		if _, err := cmd.Write(msg); err != nil {
			return err
		}
		if err := cmd.Close(); err != nil {
			return err
		}
		_, err := cmd.Wait()
		return err
	})
	if err != nil {
		t.Fatalf("append de mensagem de teste (com data) em %s: %v", mailbox, err)
	}
}

// doSearchRequest monta a requisição pra GET /emails/search. query é a
// query string sem o "?" (ex: "folder=INBOX&subject=foo"), ou "".
func doSearchRequest(t *testing.T, query, user, password string) *http.Response {
	t.Helper()

	reqURL := server.URL + "/emails/search"
	if query != "" {
		reqURL += "?" + query
	}

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
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

func decodeSearchResponse(t *testing.T, resp *http.Response) searchResponse {
	t.Helper()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	var body searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}
	return body
}
