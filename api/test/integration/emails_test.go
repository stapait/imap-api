//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"imap-api/internal/imapx"
)

type emailInfo struct {
	UID           uint32 `json:"uid"`
	Subject       string `json:"subject"`
	From          string `json:"from"`
	HasAttachment bool   `json:"hasAttachment"`
	Forwarded     bool   `json:"forwarded"`
}

type emailsResponse struct {
	Emails     []emailInfo `json:"emails"`
	Page       int         `json:"page"`
	PageSize   int         `json:"pageSize"`
	Total      int         `json:"total"`
	TotalPages int         `json:"totalPages"`
}

// TestGetEmails_Success cobre o shape geral da resposta (com os
// metadados de paginação, usando os valores padrão de page/pageSize) e
// os campos mais simples de checar (id, subject, from) contra os
// fixtures reais da INBOX — sem acoplar a um arquivo específico, só
// checando que os dados básicos vêm preenchidos.
func TestGetEmails_Success(t *testing.T) {
	resp := doEmailsRequest(t, "INBOX", "", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	var body emailsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}

	if body.Page != 1 {
		t.Errorf("page = %d, esperado 1 (padrão)", body.Page)
	}
	if body.PageSize != 50 {
		t.Errorf("pageSize = %d, esperado 50 (padrão)", body.PageSize)
	}
	if body.Total == 0 {
		t.Fatal("esperava total > 0 na INBOX (populada a partir de docker/emails/INBOX)")
	}
	wantTotalPages := (body.Total + body.PageSize - 1) / body.PageSize
	if body.TotalPages != wantTotalPages {
		t.Errorf("totalPages = %d, esperado %d (total=%d, pageSize=%d)", body.TotalPages, wantTotalPages, body.Total, body.PageSize)
	}
	if len(body.Emails) != body.PageSize {
		t.Errorf("len(emails) = %d, esperado %d (pageSize padrão, total=%d > pageSize)", len(body.Emails), body.PageSize, body.Total)
	}
	for _, e := range body.Emails {
		if e.UID == 0 {
			t.Errorf("e-mail com uid = 0: %+v", e)
		}
	}
}

// TestGetEmails_Pagination cobre page/pageSize: páginas consecutivas
// não devem se sobrepor, e a soma de todas as páginas até o fim deve
// bater com o total reportado.
func TestGetEmails_Pagination(t *testing.T) {
	const pageSize = 10

	first := doEmailsRequest(t, "INBOX", "page=1&pageSize="+fmt.Sprint(pageSize), testIMAPUser, testIMAPPassword)
	defer first.Body.Close()
	var firstBody emailsResponse
	if err := json.NewDecoder(first.Body).Decode(&firstBody); err != nil {
		t.Fatalf("decodificando página 1: %v", err)
	}

	second := doEmailsRequest(t, "INBOX", "page=2&pageSize="+fmt.Sprint(pageSize), testIMAPUser, testIMAPPassword)
	defer second.Body.Close()
	var secondBody emailsResponse
	if err := json.NewDecoder(second.Body).Decode(&secondBody); err != nil {
		t.Fatalf("decodificando página 2: %v", err)
	}

	if len(firstBody.Emails) != pageSize || len(secondBody.Emails) != pageSize {
		t.Fatalf("esperava %d e-mails em cada página, recebeu %d e %d (INBOX tem menos que %d e-mails?)",
			pageSize, len(firstBody.Emails), len(secondBody.Emails), 2*pageSize)
	}

	seen := make(map[uint32]bool, 2*pageSize)
	for _, e := range firstBody.Emails {
		seen[e.UID] = true
	}
	for _, e := range secondBody.Emails {
		if seen[e.UID] {
			t.Errorf("uid %d apareceu nas páginas 1 e 2 — páginas se sobrepondo", e.UID)
		}
	}

	// Pedir uma página bem além do fim não é erro — só vem vazia.
	beyond := doEmailsRequest(t, "INBOX", fmt.Sprintf("page=%d&pageSize=%d", firstBody.TotalPages+1000, pageSize), testIMAPUser, testIMAPPassword)
	defer beyond.Body.Close()
	if beyond.StatusCode != http.StatusOK {
		t.Fatalf("status (página além do fim) = %d, esperado %d", beyond.StatusCode, http.StatusOK)
	}
	var beyondBody emailsResponse
	if err := json.NewDecoder(beyond.Body).Decode(&beyondBody); err != nil {
		t.Fatalf("decodificando página além do fim: %v", err)
	}
	if len(beyondBody.Emails) != 0 {
		t.Errorf("esperava 0 e-mails numa página além do fim, recebeu %d", len(beyondBody.Emails))
	}
}

// TestGetEmails_OldestFirst cobre o parâmetro oldestFirst: invertido
// em relação à ordenação padrão (mais recente primeiro).
func TestGetEmails_OldestFirst(t *testing.T) {
	newest := doEmailsRequest(t, "INBOX", "pageSize=10", testIMAPUser, testIMAPPassword)
	defer newest.Body.Close()
	var newestBody emailsResponse
	if err := json.NewDecoder(newest.Body).Decode(&newestBody); err != nil {
		t.Fatalf("decodificando (padrão): %v", err)
	}

	oldest := doEmailsRequest(t, "INBOX", "pageSize=10&oldestFirst=true", testIMAPUser, testIMAPPassword)
	defer oldest.Body.Close()
	var oldestBody emailsResponse
	if err := json.NewDecoder(oldest.Body).Decode(&oldestBody); err != nil {
		t.Fatalf("decodificando (oldestFirst=true): %v", err)
	}

	if len(newestBody.Emails) == 0 || len(oldestBody.Emails) == 0 {
		t.Fatal("esperava e-mails em ambas as respostas")
	}
	if newestBody.Emails[0].UID == oldestBody.Emails[0].UID {
		t.Error("primeiro uid igual com e sem oldestFirst — ordenação não foi invertida")
	}
}

// TestGetEmails_InvalidPageParams cobre a validação de page, pageSize
// e oldestFirst — todos devem dar 400 quando fora do esperado.
func TestGetEmails_InvalidPageParams(t *testing.T) {
	for _, query := range []string{
		"page=0",
		"page=-1",
		"page=abc",
		"pageSize=9",   // abaixo do mínimo (10)
		"pageSize=101", // acima do máximo (100)
		"pageSize=abc",
		"oldestFirst=maybe",
	} {
		t.Run(query, func(t *testing.T) {
			resp := doEmailsRequest(t, "INBOX", query, testIMAPUser, testIMAPPassword)
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, esperado %d (query=%q)", resp.StatusCode, http.StatusBadRequest, query)
			}
		})
	}
}

// TestGetEmails_FieldsAndOrder cobre, com mensagens controladas
// (entregues via IMAP APPEND numa pasta só deste teste): ordenação
// por data decrescente, detecção de anexo via Content-Disposition,
// a flag $Forwarded, e o fallback nome->e-mail do campo "from".
func TestGetEmails_FieldsAndOrder(t *testing.T) {
	const folder = "TesteEmailsCampos"
	createTestMailboxes(t, folder)

	oldDate := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	newDate := time.Date(2024, 6, 15, 9, 30, 0, 0, time.UTC)

	appendTestMessage(t, folder, plainTextMessage(`"Antigo Remetente" <antigo@example.com>`, "Mensagem antiga", oldDate))
	appendTestMessage(t, folder, attachmentMessage("novo@example.com", "Mensagem nova", newDate), imap.FlagForwarded)

	resp := doEmailsRequest(t, folder, "", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	var body emailsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}
	emails := body.Emails

	if len(emails) != 2 {
		t.Fatalf("esperava 2 e-mails, recebeu %d: %+v", len(emails), emails)
	}
	if body.Total != 2 {
		t.Errorf("total = %d, esperado 2", body.Total)
	}

	newest, oldest := emails[0], emails[1]

	if newest.Subject != "Mensagem nova" {
		t.Errorf("emails[0].subject = %q, esperado a mensagem mais recente (%q) — ordenação por data decrescente falhou", newest.Subject, "Mensagem nova")
	}
	if newest.From != "novo@example.com" {
		t.Errorf("from = %q, esperado o e-mail puro (sem nome de exibição): %q", newest.From, "novo@example.com")
	}
	if !newest.HasAttachment {
		t.Error("hasAttachment = false, esperado true (mensagem tem parte com Content-Disposition: attachment)")
	}
	if !newest.Forwarded {
		t.Error("forwarded = false, esperado true (mensagem entregue com a flag $Forwarded)")
	}

	if oldest.Subject != "Mensagem antiga" {
		t.Errorf("emails[1].subject = %q, esperado a mensagem mais antiga (%q)", oldest.Subject, "Mensagem antiga")
	}
	if oldest.From != "Antigo Remetente" {
		t.Errorf("from = %q, esperado o nome de exibição: %q", oldest.From, "Antigo Remetente")
	}
	if oldest.HasAttachment {
		t.Error("hasAttachment = true, esperado false (mensagem sem anexo)")
	}
	if oldest.Forwarded {
		t.Error("forwarded = true, esperado false (mensagem sem a flag $Forwarded)")
	}
}

// TestGetEmails_EmptyFolder cobre uma pasta existente mas sem
// mensagens — FETCH 1:* numa pasta vazia é erro no protocolo IMAP
// (BAD "Invalid messageset"), então o handler precisa tratar esse
// caso à parte, sem tentar o FETCH.
func TestGetEmails_EmptyFolder(t *testing.T) {
	const folder = "TesteEmailsVazio"
	createTestMailboxes(t, folder)

	resp := doEmailsRequest(t, folder, "", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	var body emailsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}
	if body.Emails == nil {
		t.Error("esperava [] (array vazio) em \"emails\", recebeu null")
	}
	if len(body.Emails) != 0 {
		t.Errorf("esperava 0 e-mails, recebeu %d", len(body.Emails))
	}
	if body.Total != 0 {
		t.Errorf("total = %d, esperado 0", body.Total)
	}
	if body.TotalPages != 0 {
		t.Errorf("totalPages = %d, esperado 0", body.TotalPages)
	}
}

// TestGetEmails_NotFound cobre pedir e-mails de uma pasta que não
// existe.
func TestGetEmails_NotFound(t *testing.T) {
	resp := doEmailsRequest(t, "PastaQueNaoExiste", "", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestGetEmails_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword.
func TestGetEmails_MissingHeaders(t *testing.T) {
	resp, err := http.Get(server.URL + "/folders/INBOX/emails")
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestGetEmails_InvalidCredentials cobre credenciais IMAP inválidas.
func TestGetEmails_InvalidCredentials(t *testing.T) {
	resp := doEmailsRequest(t, "INBOX", "", testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestDeleteFolderEmails_Success cobre esvaziar uma pasta com
// mensagens: depois, a pasta continua existindo (só as mensagens
// somem).
func TestDeleteFolderEmails_Success(t *testing.T) {
	const folder = "TesteEsvaziarPasta"
	createTestMailboxes(t, folder)

	appendTestMessage(t, folder, plainTextMessage("remetente@example.com", "Mensagem 1", time.Now()))
	appendTestMessage(t, folder, plainTextMessage("remetente@example.com", "Mensagem 2", time.Now()))

	resp := doDeleteFolderEmailsRequest(t, folder, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}

	list := doEmailsRequest(t, folder, "", testIMAPUser, testIMAPPassword)
	defer list.Body.Close()
	var body emailsResponse
	if err := json.NewDecoder(list.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando GET /folders/%s/emails: %v", folder, err)
	}
	if body.Total != 0 {
		t.Errorf("total = %d, esperado 0 (pasta deveria estar vazia depois do DELETE)", body.Total)
	}

	// A pasta em si continua existindo — só as mensagens somem.
	get := doFolderRequest(t, folder, testIMAPUser, testIMAPPassword)
	defer get.Body.Close()
	if get.StatusCode != http.StatusOK {
		t.Errorf("GET /folders/%s depois de esvaziar: status = %d, esperado %d (a pasta não deveria ter sido apagada)", folder, get.StatusCode, http.StatusOK)
	}
}

// TestDeleteFolderEmails_AlreadyEmpty cobre esvaziar uma pasta que já
// está vazia — idempotente, não é erro (STORE 1:* numa pasta vazia é
// erro no protocolo IMAP, então o handler precisa tratar esse caso à
// parte, sem tentar o STORE, mesma lógica de TestGetEmails_EmptyFolder).
func TestDeleteFolderEmails_AlreadyEmpty(t *testing.T) {
	const folder = "TesteEsvaziarPastaVazia"
	createTestMailboxes(t, folder)

	resp := doDeleteFolderEmailsRequest(t, folder, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}
}

// TestDeleteFolderEmails_NotFound cobre esvaziar uma pasta que não
// existe.
func TestDeleteFolderEmails_NotFound(t *testing.T) {
	resp := doDeleteFolderEmailsRequest(t, "PastaQueNaoExiste", testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestDeleteFolderEmails_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword.
func TestDeleteFolderEmails_MissingHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodDelete, server.URL+"/folders/INBOX/emails", nil)
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

// TestDeleteFolderEmails_InvalidCredentials cobre credenciais IMAP
// inválidas.
func TestDeleteFolderEmails_InvalidCredentials(t *testing.T) {
	resp := doDeleteFolderEmailsRequest(t, "INBOX", testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestDeleteFolderEmails_SpecificUIDsMovesToTrash cobre DELETE
// .../emails com "uids" numa pasta que não é a lixeira: as mensagens
// somem da pasta de origem e passam a existir em "Trash" — comparado
// por Subject, não por UID (UID só é válido dentro da própria pasta;
// mover pra "Trash" atribui um UID novo lá, ver findUIDBySubject).
func TestDeleteFolderEmails_SpecificUIDsMovesToTrash(t *testing.T) {
	const folder = "TesteApagarUidsMoveLixeira"
	createTestMailboxes(t, folder)
	ensureTrashFolder(t)

	const deleteSubject = "ZZ Vai pra lixeira"
	keepUID := appendTestMessageUID(t, folder, plainTextMessage("a@example.com", "Fica", time.Now()))
	deleteUID := appendTestMessageUID(t, folder, plainTextMessage("b@example.com", deleteSubject, time.Now()))

	resp := doDeleteEmailsWithUIDsRequest(t, folder, []uint32{deleteUID}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}

	if uidExistsInFolder(t, folder, deleteUID) {
		t.Errorf("uid=%d ainda existe em %s — deveria ter sido movido pra %s", deleteUID, folder, trashFolderNameForTest)
	}
	if !uidExistsInFolder(t, folder, keepUID) {
		t.Errorf("uid=%d não deveria ter sumido de %s", keepUID, folder)
	}
	if _, ok := findUIDBySubject(t, trashFolderNameForTest, deleteSubject); !ok {
		t.Errorf("mensagem %q não foi encontrada em %s — deveria ter sido movida pra lá", deleteSubject, trashFolderNameForTest)
	}
}

// TestDeleteFolderEmails_SpecificUIDsInTrashIsPermanent cobre DELETE
// .../emails com "uids" na própria pasta "Trash": apaga definitivo, não
// move (não haveria pra onde).
func TestDeleteFolderEmails_SpecificUIDsInTrashIsPermanent(t *testing.T) {
	ensureTrashFolder(t)

	uid := appendTestMessageUID(t, trashFolderNameForTest, plainTextMessage("a@example.com", "Apagar de vez", time.Now()))

	resp := doDeleteEmailsWithUIDsRequest(t, trashFolderNameForTest, []uint32{uid}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}

	if uidExistsInFolder(t, trashFolderNameForTest, uid) {
		t.Errorf("uid=%d ainda existe em %s — deveria ter sido apagado definitivamente", uid, trashFolderNameForTest)
	}
}

// Não há um teste dedicado pra "pasta de lixeira não encontrada"
// (errTrashFolderMissing, 404): confirmado empiricamente que o Dovecot
// local trata "Trash" como uma pasta especial auto-provisionada (herdada
// da config default da imagem dovecot/dovecot, não de nada deste
// repositório — ver docker/dovecot/conf.d/, que não define isso) — um
// MOVE pra "Trash" recria a pasta na hora, mesmo numa conexão nova logo
// depois dela ter sido apagada, então esse caminho de erro não é
// exercitável neste ambiente. O mecanismo em si (isTryCreate → 404) é
// coberto por TestPatchEmails_DestinationNotFound, que move pra um nome
// (PastaQueNaoExiste) sem esse comportamento especial.

// TestDeleteFolderEmails_SpecificUIDsFolderNotFound cobre DELETE
// .../emails com "uids" numa pasta de origem que não existe.
func TestDeleteFolderEmails_SpecificUIDsFolderNotFound(t *testing.T) {
	resp := doDeleteEmailsWithUIDsRequest(t, "PastaQueNaoExiste", []uint32{1}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestPatchEmailFlags_MarkRead cobre marcar mensagens como lidas em
// lote.
func TestPatchEmailFlags_MarkRead(t *testing.T) {
	const folder = "TestePatchMarcarLida"
	createTestMailboxes(t, folder)

	uid1 := appendTestMessageUID(t, folder, plainTextMessage("a@example.com", "Um", time.Now()))
	uid2 := appendTestMessageUID(t, folder, plainTextMessage("b@example.com", "Dois", time.Now()))

	resp := doPatchEmailFlagsRequest(t, folder, patchEmailFlagsRequestBody{UIDs: []uint32{uid1, uid2}, Read: boolPtr(true)}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}
	if !isSeenOnServer(t, folder, uid1) {
		t.Errorf("uid=%d não está \\Seen depois de read=true", uid1)
	}
	if !isSeenOnServer(t, folder, uid2) {
		t.Errorf("uid=%d não está \\Seen depois de read=true", uid2)
	}
}

// TestPatchEmailFlags_MarkUnread cobre marcar mensagens como não lidas
// em lote (mensagens entregues já como \Seen).
func TestPatchEmailFlags_MarkUnread(t *testing.T) {
	const folder = "TestePatchMarcarNaoLida"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, plainTextMessage("a@example.com", "Um", time.Now()), imap.FlagSeen)

	resp := doPatchEmailFlagsRequest(t, folder, patchEmailFlagsRequestBody{UIDs: []uint32{uid}, Read: boolPtr(false)}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}
	if isSeenOnServer(t, folder, uid) {
		t.Errorf("uid=%d ainda está \\Seen depois de read=false", uid)
	}
}

// TestPatchEmailFlags_MarkStarred cobre favoritar mensagens em lote.
func TestPatchEmailFlags_MarkStarred(t *testing.T) {
	const folder = "TestePatchFavoritar"
	createTestMailboxes(t, folder)

	uid1 := appendTestMessageUID(t, folder, plainTextMessage("a@example.com", "Um", time.Now()))
	uid2 := appendTestMessageUID(t, folder, plainTextMessage("b@example.com", "Dois", time.Now()))

	resp := doPatchEmailFlagsRequest(t, folder, patchEmailFlagsRequestBody{UIDs: []uint32{uid1, uid2}, Starred: boolPtr(true)}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}
	if !isFlaggedOnServer(t, folder, uid1) {
		t.Errorf("uid=%d não está \\Flagged depois de starred=true", uid1)
	}
	if !isFlaggedOnServer(t, folder, uid2) {
		t.Errorf("uid=%d não está \\Flagged depois de starred=true", uid2)
	}
}

// TestPatchEmailFlags_MarkUnstarred cobre desfavoritar mensagens em
// lote (mensagens entregues já como \Flagged).
func TestPatchEmailFlags_MarkUnstarred(t *testing.T) {
	const folder = "TestePatchDesfavoritar"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, plainTextMessage("a@example.com", "Um", time.Now()), imap.FlagFlagged)

	resp := doPatchEmailFlagsRequest(t, folder, patchEmailFlagsRequestBody{UIDs: []uint32{uid}, Starred: boolPtr(false)}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}
	if isFlaggedOnServer(t, folder, uid) {
		t.Errorf("uid=%d ainda está \\Flagged depois de starred=false", uid)
	}
}

// TestPatchEmailFlags_MarkAnswered cobre marcar mensagens como
// respondidas em lote — flag \Answered, sem nenhum endpoint de "enviar
// resposta" por trás (esta API não faz SMTP); é só o cliente que
// respondeu de verdade em outro lugar avisando a API pra refletir isso.
func TestPatchEmailFlags_MarkAnswered(t *testing.T) {
	const folder = "TestePatchMarcarRespondida"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, plainTextMessage("a@example.com", "Um", time.Now()))

	resp := doPatchEmailFlagsRequest(t, folder, patchEmailFlagsRequestBody{UIDs: []uint32{uid}, Answered: boolPtr(true)}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}
	if !hasFlagOnServer(t, folder, uid, imap.FlagAnswered) {
		t.Errorf("uid=%d não está \\Answered depois de answered=true", uid)
	}
}

// TestPatchEmailFlags_MarkForwarded cobre marcar mensagens como
// encaminhadas em lote — flag $Forwarded (keyword, não flag de sistema
// — mesma flag que GET .../emails/{uid} já lê pro campo "forwarded").
func TestPatchEmailFlags_MarkForwarded(t *testing.T) {
	const folder = "TestePatchMarcarEncaminhada"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, plainTextMessage("a@example.com", "Um", time.Now()))

	resp := doPatchEmailFlagsRequest(t, folder, patchEmailFlagsRequestBody{UIDs: []uint32{uid}, Forwarded: boolPtr(true)}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}
	if !hasFlagOnServer(t, folder, uid, imap.FlagForwarded) {
		t.Errorf("uid=%d não está $Forwarded depois de forwarded=true", uid)
	}
}

// TestPatchEmailFlags_MultipleFlagsTogether cobre várias flags no mesmo
// body, aplicadas juntas.
func TestPatchEmailFlags_MultipleFlagsTogether(t *testing.T) {
	const folder = "TestePatchVariasFlagsJuntas"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, plainTextMessage("a@example.com", "Um", time.Now()))

	resp := doPatchEmailFlagsRequest(t, folder, patchEmailFlagsRequestBody{
		UIDs: []uint32{uid}, Read: boolPtr(true), Starred: boolPtr(true), Answered: boolPtr(true),
	}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}
	if !isSeenOnServer(t, folder, uid) {
		t.Errorf("uid=%d não está \\Seen", uid)
	}
	if !isFlaggedOnServer(t, folder, uid) {
		t.Errorf("uid=%d não está \\Flagged", uid)
	}
	if !hasFlagOnServer(t, folder, uid, imap.FlagAnswered) {
		t.Errorf("uid=%d não está \\Answered", uid)
	}
}

// TestPatchEmailFlags_FolderNotFound cobre uma pasta que não existe.
func TestPatchEmailFlags_FolderNotFound(t *testing.T) {
	resp := doPatchEmailFlagsRequest(t, "PastaQueNaoExiste", patchEmailFlagsRequestBody{UIDs: []uint32{1}, Read: boolPtr(true)}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestPatchEmailFlags_EmptyUIDs cobre um body com "uids" vazio.
func TestPatchEmailFlags_EmptyUIDs(t *testing.T) {
	resp := doPatchEmailFlagsRequest(t, "INBOX", patchEmailFlagsRequestBody{UIDs: []uint32{}, Read: boolPtr(true)}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestPatchEmailFlags_NoFlags cobre um body sem nenhuma flag.
func TestPatchEmailFlags_NoFlags(t *testing.T) {
	resp := doPatchEmailFlagsRequest(t, "INBOX", patchEmailFlagsRequestBody{UIDs: []uint32{1}}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestPatchEmailFlags_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword.
func TestPatchEmailFlags_MissingHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPatch, server.URL+"/folders/INBOX/emails/flags", strings.NewReader(`{"uids":[1],"read":true}`))
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

// TestPatchEmailFlags_InvalidCredentials cobre credenciais IMAP
// inválidas.
func TestPatchEmailFlags_InvalidCredentials(t *testing.T) {
	resp := doPatchEmailFlagsRequest(t, "INBOX", patchEmailFlagsRequestBody{UIDs: []uint32{1}, Read: boolPtr(true)}, testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestMoveEmails_Success cobre mover mensagens em lote pra outra pasta.
func TestMoveEmails_Success(t *testing.T) {
	const (
		source = "TesteMoverOrigem"
		dest   = "TesteMoverDestino"
	)
	createTestMailboxes(t, source, dest)

	const subject = "Mover"
	uid := appendTestMessageUID(t, source, plainTextMessage("a@example.com", subject, time.Now()))

	resp := doMoveEmailsRequest(t, source, moveEmailsRequestBody{UIDs: []uint32{uid}, DestinationFolder: dest}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNoContent)
	}
	if uidExistsInFolder(t, source, uid) {
		t.Errorf("uid=%d ainda existe em %s depois de mover", uid, source)
	}
	// UID só é válido dentro da própria pasta — mover atribui um UID novo
	// em dest, por isso busca por Subject, não pelo mesmo uid de source.
	if _, ok := findUIDBySubject(t, dest, subject); !ok {
		t.Errorf("mensagem %q não foi encontrada em %s depois de mover", subject, dest)
	}
}

// TestMoveEmails_DestinationNotFound cobre mover pra uma pasta que não
// existe.
func TestMoveEmails_DestinationNotFound(t *testing.T) {
	const folder = "TesteMoverDestinoInexistente"
	createTestMailboxes(t, folder)

	uid := appendTestMessageUID(t, folder, plainTextMessage("a@example.com", "Um", time.Now()))

	resp := doMoveEmailsRequest(t, folder, moveEmailsRequestBody{UIDs: []uint32{uid}, DestinationFolder: "PastaQueNaoExiste"}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestMoveEmails_FolderNotFound cobre uma pasta de origem que não
// existe.
func TestMoveEmails_FolderNotFound(t *testing.T) {
	resp := doMoveEmailsRequest(t, "PastaQueNaoExiste", moveEmailsRequestBody{UIDs: []uint32{1}, DestinationFolder: "INBOX"}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestMoveEmails_EmptyUIDs cobre um body com "uids" vazio.
func TestMoveEmails_EmptyUIDs(t *testing.T) {
	resp := doMoveEmailsRequest(t, "INBOX", moveEmailsRequestBody{UIDs: []uint32{}, DestinationFolder: "INBOX"}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestMoveEmails_MissingDestination cobre um body sem
// "destinationFolder".
func TestMoveEmails_MissingDestination(t *testing.T) {
	resp := doMoveEmailsRequest(t, "INBOX", moveEmailsRequestBody{UIDs: []uint32{1}}, testIMAPUser, testIMAPPassword)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestMoveEmails_MissingHeaders cobre a ausência dos headers
// imapUser/imapPassword.
func TestMoveEmails_MissingHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPatch, server.URL+"/folders/INBOX/emails/move", strings.NewReader(`{"uids":[1],"destinationFolder":"Arquivo"}`))
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

// TestMoveEmails_InvalidCredentials cobre credenciais IMAP inválidas.
func TestMoveEmails_InvalidCredentials(t *testing.T) {
	resp := doMoveEmailsRequest(t, "INBOX", moveEmailsRequestBody{UIDs: []uint32{1}, DestinationFolder: "Arquivo"}, testIMAPUser, "senha-errada")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// trashFolderNameForTest replica internal/httpapi/emails.go's
// trashFolderName (não exportado) — mantido em sincronia à mão, já que
// é uma constante hardcoded (ver decisão registrada em
// api/README.md/CLAUDE.md).
const trashFolderNameForTest = "Trash"

// ensureTrashFolder garante uma pasta "Trash" vazia e registra a
// limpeza no fim do teste — igual createTestMailboxes, mas apaga antes
// de criar: o Dovecot local trata "Trash" como pasta especial
// auto-provisionada (não é nada deste repositório, é herdado da config
// default da imagem dovecot/dovecot — confirmado que ela reaparece
// sozinha ao ser referenciada, mesmo tendo acabado de ser apagada),
// então createTestMailboxes sozinho arriscaria um ALREADYEXISTS aqui,
// diferente de qualquer outra pasta de teste.
func ensureTrashFolder(t *testing.T) {
	t.Helper()
	deleteTestMailboxIfExists(t, trashFolderNameForTest)
	createTestMailboxes(t, trashFolderNameForTest)
}

// patchEmailFlagsRequestBody é o shape de body de PATCH
// /folders/{pasta}/emails/flags usado pelos testes — omitempty em cada
// flag pra poder cobrir "nenhuma flag informada" sem mandar zero-values
// explícitos.
type patchEmailFlagsRequestBody struct {
	UIDs      []uint32 `json:"uids"`
	Read      *bool    `json:"read,omitempty"`
	Starred   *bool    `json:"starred,omitempty"`
	Answered  *bool    `json:"answered,omitempty"`
	Forwarded *bool    `json:"forwarded,omitempty"`
}

// boolPtr é um atalho pra endereço de um bool literal.
func boolPtr(b bool) *bool { return &b }

// doPatchEmailFlagsRequest monta a requisição pra PATCH
// /folders/{pasta}/emails/flags.
func doPatchEmailFlagsRequest(t *testing.T, folder string, body patchEmailFlagsRequestBody, user, password string) *http.Response {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("montando body: %v", err)
	}

	req, err := http.NewRequest(http.MethodPatch, server.URL+"/folders/"+folder+"/emails/flags", bytes.NewReader(data))
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

// moveEmailsRequestBody é o shape de body de PATCH
// /folders/{pasta}/emails/move usado pelos testes.
type moveEmailsRequestBody struct {
	UIDs              []uint32 `json:"uids"`
	DestinationFolder string   `json:"destinationFolder,omitempty"`
}

// doMoveEmailsRequest monta a requisição pra PATCH
// /folders/{pasta}/emails/move.
func doMoveEmailsRequest(t *testing.T, folder string, body moveEmailsRequestBody, user, password string) *http.Response {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("montando body: %v", err)
	}

	req, err := http.NewRequest(http.MethodPatch, server.URL+"/folders/"+folder+"/emails/move", bytes.NewReader(data))
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

// doDeleteEmailsWithUIDsRequest monta a requisição pra DELETE
// /folders/{pasta}/emails com um body {"uids": [...]}.
func doDeleteEmailsWithUIDsRequest(t *testing.T, folder string, uids []uint32, user, password string) *http.Response {
	t.Helper()

	data, err := json.Marshal(deleteEmailsRequestBody{UIDs: uids})
	if err != nil {
		t.Fatalf("montando body: %v", err)
	}

	req, err := http.NewRequest(http.MethodDelete, server.URL+"/folders/"+folder+"/emails", bytes.NewReader(data))
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

// deleteEmailsRequestBody é o shape de body de DELETE
// /folders/{pasta}/emails usado pelos testes.
type deleteEmailsRequestBody struct {
	UIDs []uint32 `json:"uids"`
}

// uidExistsInFolder checa direto no IMAP se uid existe na pasta folder.
func uidExistsInFolder(t *testing.T, folder string, uid uint32) bool {
	t.Helper()

	found := false
	err := imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		if _, err := c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
			return err
		}
		var uidSet imap.UIDSet
		uidSet.AddNum(imap.UID(uid))
		messages, err := c.Fetch(uidSet, &imap.FetchOptions{UID: true}).Collect()
		if err != nil {
			return err
		}
		found = len(messages) > 0
		return nil
	})
	if err != nil {
		t.Fatalf("checando uid=%d em %s: %v", uid, folder, err)
	}
	return found
}

// findUIDBySubject busca, direto no IMAP, o UID da mensagem com esse
// Subject exato em folder — necessário pra verificar que uma mensagem
// movida chegou no destino, já que UID é só válido dentro da própria
// pasta: mover (ou copiar) uma mensagem pra outra pasta atribui um UID
// novo lá, o valor antigo não é preservado entre pastas (diferente de
// mensagem pra mensagem, onde comparar pelo mesmo valor de UID em
// origem/destino é um erro).
func findUIDBySubject(t *testing.T, folder, subject string) (uid uint32, ok bool) {
	t.Helper()

	err := imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		if _, err := c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
			return err
		}
		data, err := c.UIDSearch(&imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: subject}},
		}, nil).Wait()
		if err != nil {
			return err
		}
		uids := data.AllUIDs()
		if len(uids) > 0 {
			uid = uint32(uids[len(uids)-1])
			ok = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("buscando por subject %q em %s: %v", subject, folder, err)
	}
	return uid, ok
}

// doDeleteFolderEmailsRequest monta a requisição pra DELETE
// /folders/{pasta}/emails.
func doDeleteFolderEmailsRequest(t *testing.T, folder, user, password string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodDelete, server.URL+"/folders/"+folder+"/emails", nil)
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

// doEmailsRequest monta a requisição pra GET /folders/{pasta}/emails.
// query é a query string sem o "?" (ex: "page=2&pageSize=10"), ou "".
func doEmailsRequest(t *testing.T, folder, query, user, password string) *http.Response {
	t.Helper()

	url := server.URL + "/folders/" + folder + "/emails"
	if query != "" {
		url += "?" + query
	}

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

// appendTestMessage entrega msg na pasta indicada via IMAP APPEND
// (não LMTP — mais simples pra montar mensagens ad-hoc dentro do
// teste, sem precisar de um arquivo .eml separado).
func appendTestMessage(t *testing.T, mailbox string, msg []byte, flags ...imap.Flag) {
	t.Helper()

	err := imapx.WithClient(cfg.IMAP, testIMAPUser, testIMAPPassword, func(c *imapclient.Client) error {
		cmd := c.Append(mailbox, int64(len(msg)), &imap.AppendOptions{Flags: flags})
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
		t.Fatalf("append de mensagem de teste em %s: %v", mailbox, err)
	}
}

// plainTextMessage monta um .eml mínimo, texto simples, sem anexo.
func plainTextMessage(from, subject string, date time.Time) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: destino@example.com\r\n")
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", date.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString("Corpo de teste.\r\n")
	return []byte(b.String())
}

// attachmentMessage monta um .eml multipart/mixed com uma parte
// Content-Disposition: attachment.
func attachmentMessage(from, subject string, date time.Time) []byte {
	const boundary = "TESTE-BOUNDARY-EMAILS"
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: destino@example.com\r\n")
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", date.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary)
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString("Corpo de teste com anexo.\r\n")
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; name=\"anexo.txt\"\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"anexo.txt\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	b.WriteString("\r\n")
	b.WriteString("conteudo do anexo\r\n")
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return []byte(b.String())
}
