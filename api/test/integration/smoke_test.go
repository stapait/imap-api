//go:build integration

// Infraestrutura compartilhada pros testes de "fumaça": iteram TODA
// mensagem real da caixa de teste (docker/emails/ + docker/emails-
// permanent/, sempre repopulados por TestMain — incluindo o corpus
// real do SpamAssassin, cheio de formatos "traiçoeiros" difícil de
// prever à mão) contra um endpoint que opera sobre uma mensagem
// específica, garantindo que nenhuma delas derruba o endpoint com
// 5xx. Isso é o que de fato exercita a diversidade/malformação do
// corpus contra o parser — os testes com mensagens sintéticas (ver
// richMessage etc. em email_test.go) cobrem casos específicos
// conhecidos, mas não substituem isso.
//
// Convenção: todo endpoint novo que receba {pasta}/{uid} (ou
// equivalente, uma mensagem específica) deve ter um teste de fumaça
// assim — ver TestGetEmail_SmokeAllMessages em email_test.go pro
// padrão a seguir.
package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// folderEmailRef identifica uma mensagem real da caixa de teste por
// pasta+UID.
type folderEmailRef struct {
	Folder string
	UID    uint32
}

// allFolderEmailRefs lista, batendo na própria API (GET /folders e GET
// /folders/{pasta}/emails, paginado), toda pasta com mensagens e todo
// UID de mensagem dentro delas.
func allFolderEmailRefs(t *testing.T) []folderEmailRef {
	t.Helper()

	foldersResp := doFoldersRequest(t, testIMAPUser, testIMAPPassword)
	defer foldersResp.Body.Close()
	if foldersResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /folders: status = %d, esperado %d", foldersResp.StatusCode, http.StatusOK)
	}
	var folders foldersResponse
	if err := json.NewDecoder(foldersResp.Body).Decode(&folders); err != nil {
		t.Fatalf("decodificando GET /folders: %v", err)
	}

	const pageSize = 100
	var refs []folderEmailRef
	for _, f := range folders.Folders {
		if f.Messages == 0 {
			continue
		}
		for page := 1; ; page++ {
			resp := doEmailsRequest(t, f.Name, fmt.Sprintf("page=%d&pageSize=%d", page, pageSize), testIMAPUser, testIMAPPassword)
			var body emailsResponse
			decodeErr := json.NewDecoder(resp.Body).Decode(&body)
			status := resp.StatusCode
			resp.Body.Close()

			if status != http.StatusOK {
				t.Fatalf("GET /folders/%s/emails (page=%d): status = %d, esperado %d", f.Name, page, status, http.StatusOK)
			}
			if decodeErr != nil {
				t.Fatalf("decodificando GET /folders/%s/emails (page=%d): %v", f.Name, page, decodeErr)
			}

			for _, e := range body.Emails {
				refs = append(refs, folderEmailRef{Folder: f.Name, UID: e.UID})
			}
			if len(body.Emails) < pageSize {
				break
			}
		}
	}
	return refs
}

// smokeTestConcurrency limita quantas chamadas simultâneas os testes
// de fumaça fazem contra a API. Cada requisição abre sua própria
// conexão IMAP (ver internal/imapx.WithClient), e o Dovecot local
// limita a 10 conexões simultâneas por usuário+IP
// (mail_max_userip_connections, padrão da imagem) — acima disso o
// login passa a falhar. Fica bem abaixo desse teto pra sobrar espaço
// pra outras conexões concorrentes (ex: outro teste do pacote).
const smokeTestConcurrency = 6

// runSmokeTest roda check pra cada ref em refs como um subteste
// nomeado "pasta/uid" (assim uma falha aponta exatamente qual mensagem
// quebrou, sem abortar as demais), em paralelo — limitado a
// smokeTestConcurrency chamadas simultâneas via semáforo, já que o
// grau de paralelismo do próprio `go test` (t.Parallel, controlado
// pela flag -parallel) não tem relação com o limite de conexões do
// Dovecot.
func runSmokeTest(t *testing.T, refs []folderEmailRef, check func(t *testing.T, ref folderEmailRef)) {
	t.Helper()

	sem := make(chan struct{}, smokeTestConcurrency)
	for _, ref := range refs {
		t.Run(fmt.Sprintf("%s/%d", ref.Folder, ref.UID), func(t *testing.T) {
			t.Parallel()
			sem <- struct{}{}
			defer func() { <-sem }()
			check(t, ref)
		})
	}
}
