package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"

	"imap-api/internal/config"
	"imap-api/internal/imapx"
)

const (
	defaultPageSize = 50
	minPageSize     = 10
	maxPageSize     = 100
)

// emailInfo é a listagem leve de uma mensagem (sem corpo), usada por
// GET /folders/{pasta}/emails.
type emailInfo struct {
	UID           uint32 `json:"uid"`
	Subject       string `json:"subject"`
	From          string `json:"from"`
	HasAttachment bool   `json:"hasAttachment"`
	Forwarded     bool   `json:"forwarded"`
}

// emailsResponse é o body de GET /folders/{pasta}/emails: os e-mails
// da página pedida, mais os metadados pra montar um paginador (total
// de mensagens da pasta e de páginas).
type emailsResponse struct {
	Emails     []emailInfo `json:"emails"`
	Page       int         `json:"page"`
	PageSize   int         `json:"pageSize"`
	Total      int         `json:"total"`
	TotalPages int         `json:"totalPages"`
}

// errEmailNotFound indica que o UID pedido em GET
// /folders/{pasta}/emails/{uid} não existe na pasta.
var errEmailNotFound = errors.New("e-mail não encontrado")

// emailBodySection é o item de FETCH BODY[] pedido em
// GET /folders/{pasta}/emails/{uid} — a mensagem inteira (sem
// Specifier/Part), sem PEEK: esse endpoint faz um Select em modo
// leitura-escrita (diferente da listagem, que usa EXAMINE), então o
// próprio FETCH marca a mensagem como \Seen, igual um cliente de
// e-mail faria ao abrir uma mensagem.
var emailBodySection = &imap.FetchItemBodySection{}

// emailAddress é um remetente/destinatário no shape JSON da API.
type emailAddress struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// attachmentInfo é o metadado leve (sem o conteúdo binário) de um
// anexo, usado em GET /folders/{pasta}/emails/{uid}. AttachmentLink é
// preenchido à parte (por handleGetEmail, depois de
// emailDetailFromMessage) — precisa de pasta/uid, que não chegam até
// emailDetailFromMessage.
type attachmentInfo struct {
	Filename       string `json:"filename"`
	ContentType    string `json:"contentType"`
	Size           int64  `json:"size"`
	AttachmentLink string `json:"attachmentLink"`
}

// emailDetail é o body de GET /folders/{pasta}/emails/{uid}: os
// metadados estruturados da mensagem (equivalente ao ENVELOPE/FLAGS
// do IMAP — nunca um bloco de headers técnicos crus, tipo
// Message-ID/Received/Return-Path) mais o corpo parseado (texto ou
// HTML, o que a mensagem tiver — HTML tem preferência quando os dois
// existem) e a lista leve dos anexos (sem o conteúdo binário).
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

// paginationParams são os parâmetros de paginação/ordenação
// compartilhados por GET /folders/{pasta}/emails e GET /emails/search.
type paginationParams struct {
	page        int
	pageSize    int
	oldestFirst bool
}

// parsePaginationParams lê e valida page, pageSize e oldestFirst da
// query string. page tem que ser um inteiro >= 1 (padrão 1). pageSize
// tem que ser um inteiro entre minPageSize e maxPageSize, inclusive
// (padrão defaultPageSize). oldestFirst é opcional (padrão false).
func parsePaginationParams(query url.Values) (paginationParams, error) {
	params := paginationParams{page: 1, pageSize: defaultPageSize}

	if v := query.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return paginationParams{}, fmt.Errorf("page inválido (%q): precisa ser um inteiro >= 1", v)
		}
		params.page = n
	}

	if v := query.Get("pageSize"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < minPageSize || n > maxPageSize {
			return paginationParams{}, fmt.Errorf("pageSize inválido (%q): precisa ser um inteiro entre %d e %d", v, minPageSize, maxPageSize)
		}
		params.pageSize = n
	}

	if v := query.Get("oldestFirst"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return paginationParams{}, fmt.Errorf("oldestFirst inválido (%q): precisa ser true ou false", v)
		}
		params.oldestFirst = b
	}

	return params, nil
}

// listEmailsParams são os parâmetros de query de GET
// /folders/{pasta}/emails.
type listEmailsParams struct {
	paginationParams
}

func parseListEmailsParams(r *http.Request) (listEmailsParams, error) {
	params, err := parsePaginationParams(r.URL.Query())
	if err != nil {
		return listEmailsParams{}, err
	}
	return listEmailsParams{paginationParams: params}, nil
}

// handleListEmails implementa GET /folders/{pasta}/emails: lista as
// mensagens da pasta pedida, paginada (page/pageSize) e ordenada por
// data (mais recente primeiro, ou mais antiga primeiro com
// oldestFirst=true) — sem baixar o corpo, equivalente a um IMAP FETCH
// de UID + ENVELOPE + BODYSTRUCTURE + FLAGS.
//
// @Summary Lista e-mails de uma pasta
// @Description Listagem leve (sem corpo) pra exibição em lista, paginada e ordenada por data. Não marca as mensagens como lidas (Select em modo somente-leitura).
// @Tags e-mails
// @Produce json
// @Param pasta path string true "Nome da pasta (barra de nível aninhado como %2F)"
// @Param page query int false "Página (padrão 1)"
// @Param pageSize query int false "Itens por página, 10-100 (padrão 50)"
// @Param oldestFirst query bool false "Inverte a ordenação padrão (mais recente primeiro) pra mais antiga primeiro"
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 200 {object} emailsResponse
// @Failure 400 {object} errorResponse "nome da pasta ausente, ou page/pageSize/oldestFirst inválidos"
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 404 {object} errorResponse "pasta não encontrada"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /folders/{pasta}/emails [get]
func handleListEmails(cfg config.IMAPConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pasta := r.PathValue("pasta")
		if pasta == "" {
			writeError(w, http.StatusBadRequest, "nome da pasta é obrigatório")
			return
		}

		params, err := parseListEmailsParams(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		var messages []*imapclient.FetchMessageBuffer
		err = imapx.WithClient(cfg, creds.user, creds.password, func(c *imapclient.Client) error {
			// EXAMINE (SELECT read-only): esse endpoint só lê, nunca
			// deve ter o efeito colateral de marcar mensagens como
			// \Seen.
			selectData, err := c.Select(pasta, &imap.SelectOptions{ReadOnly: true}).Wait()
			if err != nil {
				if isNonExistentMailbox(err) {
					return errFolderNotFound
				}
				return err
			}

			if selectData.NumMessages == 0 {
				// FETCH 1:* com a pasta vazia é erro (BAD "Invalid
				// messageset") — nem chega a tentar.
				return nil
			}

			var seqSet imap.SeqSet
			seqSet.AddRange(1, 0) // 1:* — todas as mensagens da pasta

			messages, err = c.Fetch(seqSet, &imap.FetchOptions{
				UID:      true,
				Envelope: true,
				Flags:    true,
				// Extended: true é o que traz Content-Disposition (usado
				// em hasAttachment) — sem isso o servidor devolve BODY
				// em vez de BODYSTRUCTURE, sem essa informação.
				BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
			}).Collect()
			return err
		})

		if err != nil {
			if errors.Is(err, errFolderNotFound) {
				writeError(w, http.StatusNotFound, "pasta não encontrada")
				return
			}
			writeIMAPError(w, err, "erro ao listar e-mails")
			return
		}

		// O IMAP não garante nenhuma ordem por data no resultado do
		// FETCH — ordena aqui. Paginação é feita em memória sobre a
		// pasta inteira (já precisamos buscar tudo pra poder ordenar
		// por data).
		sort.Slice(messages, func(i, j int) bool {
			if params.oldestFirst {
				return envelopeDate(messages[i]).Before(envelopeDate(messages[j]))
			}
			return envelopeDate(messages[i]).After(envelopeDate(messages[j]))
		})

		writeJSON(w, http.StatusOK, paginate(messages, params))
	}
}

// handleGetEmail implementa GET /folders/{pasta}/emails/{uid}: busca a
// mensagem de UID {uid} na pasta pedida e devolve seus metadados
// (equivalente ao ENVELOPE/FLAGS) mais o corpo parseado e a lista leve
// de anexos. Diferente de handleListEmails, faz um Select em modo
// leitura-escrita — buscar o corpo aqui marca a mensagem como lida
// (\Seen), como um cliente de e-mail faria ao abrir uma mensagem.
//
// @Summary Busca um e-mail completo
// @Description E-mail completo: metadados estruturados (equivalente ao ENVELOPE/FLAGS do IMAP — nunca um bloco de headers técnicos crus, tipo Message-ID/Received/Return-Path) mais o corpo parseado (HTML tem preferência sobre texto simples quando a mensagem tem os dois) e a lista leve de anexos (nome/tipo/tamanho, sem o conteúdo binário). Diferente da listagem, marca a mensagem como lida (\Seen) no servidor IMAP.
// @Tags e-mails
// @Produce json
// @Param pasta path string true "Nome da pasta (barra de nível aninhado como %2F)"
// @Param uid path int true "UID da mensagem (IMAP)"
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 200 {object} emailDetail
// @Failure 400 {object} errorResponse "nome da pasta ausente, ou uid não numérico"
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 404 {object} errorResponse "pasta ou e-mail não encontrados"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /folders/{pasta}/emails/{uid} [get]
func handleGetEmail(cfg config.IMAPConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pasta := r.PathValue("pasta")
		if pasta == "" {
			writeError(w, http.StatusBadRequest, "nome da pasta é obrigatório")
			return
		}

		uid, err := strconv.ParseUint(r.PathValue("uid"), 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, "uid inválido: precisa ser o UID numérico da mensagem")
			return
		}

		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		var msg *imapclient.FetchMessageBuffer
		err = imapx.WithClient(cfg, creds.user, creds.password, func(c *imapclient.Client) error {
			if _, err := c.Select(pasta, nil).Wait(); err != nil {
				if isNonExistentMailbox(err) {
					return errFolderNotFound
				}
				return err
			}

			var uidSet imap.UIDSet
			uidSet.AddNum(imap.UID(uid))

			messages, err := c.Fetch(uidSet, &imap.FetchOptions{
				UID:         true,
				Envelope:    true,
				Flags:       true,
				BodySection: []*imap.FetchItemBodySection{emailBodySection},
			}).Collect()
			if err != nil {
				return err
			}
			if len(messages) == 0 {
				return errEmailNotFound
			}
			msg = messages[0]
			return nil
		})

		if err != nil {
			switch {
			case errors.Is(err, errFolderNotFound):
				writeError(w, http.StatusNotFound, "pasta não encontrada")
			case errors.Is(err, errEmailNotFound):
				writeError(w, http.StatusNotFound, "e-mail não encontrado")
			default:
				writeIMAPError(w, err, "erro ao buscar e-mail")
			}
			return
		}

		detail := emailDetailFromMessage(msg)
		// O índice de cada anexo aqui é o mesmo que findAttachment (em
		// attachments.go) usa pra localizar o anexo de volta a partir do
		// attachmentId — os dois percorrem o mesmo raw (msg.FindBodySection)
		// com o mesmo parser (go-message/mail) na mesma ordem.
		for i := range detail.Attachments {
			detail.Attachments[i].AttachmentLink = attachmentLink(pasta, uid, i, detail.Attachments[i].Filename)
		}

		writeJSON(w, http.StatusOK, detail)
	}
}

// handleGetRawEmail implementa GET /folders/{pasta}/emails/{uid}/raw:
// devolve a fonte crua da mensagem (RFC 822), sem nenhum parsing — útil
// pra debug ou export. Como o download de anexo (attachments.go), e
// diferente de handleGetEmail, faz Select em modo leitura (EXAMINE):
// baixar o raw não marca a mensagem como lida — é uma operação técnica
// de export, não "abrir a mensagem".
//
// @Summary Baixa a fonte crua do e-mail
// @Description Conteúdo bruto da mensagem original (RFC 822), sem nenhum parsing — útil pra debug ou export. Diferente de GET .../emails/{uid}, não marca a mensagem como lida.
// @Tags e-mails
// @Produce message/rfc822
// @Param pasta path string true "Nome da pasta (barra de nível aninhado como %2F)"
// @Param uid path int true "UID da mensagem (IMAP)"
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 200 {file} file
// @Failure 400 {object} errorResponse "nome da pasta ausente, ou uid não numérico"
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 404 {object} errorResponse "pasta ou e-mail não encontrados"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /folders/{pasta}/emails/{uid}/raw [get]
func handleGetRawEmail(cfg config.IMAPConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pasta := r.PathValue("pasta")
		if pasta == "" {
			writeError(w, http.StatusBadRequest, "nome da pasta é obrigatório")
			return
		}

		uid, err := strconv.ParseUint(r.PathValue("uid"), 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, "uid inválido: precisa ser o UID numérico da mensagem")
			return
		}

		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		var raw []byte
		err = imapx.WithClient(cfg, creds.user, creds.password, func(c *imapclient.Client) error {
			if _, err := c.Select(pasta, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
				if isNonExistentMailbox(err) {
					return errFolderNotFound
				}
				return err
			}

			var uidSet imap.UIDSet
			uidSet.AddNum(imap.UID(uid))

			messages, err := c.Fetch(uidSet, &imap.FetchOptions{
				BodySection: []*imap.FetchItemBodySection{emailBodySection},
			}).Collect()
			if err != nil {
				return err
			}
			if len(messages) == 0 {
				return errEmailNotFound
			}

			raw = messages[0].FindBodySection(emailBodySection)
			return nil
		})

		if err != nil {
			switch {
			case errors.Is(err, errFolderNotFound):
				writeError(w, http.StatusNotFound, "pasta não encontrada")
			case errors.Is(err, errEmailNotFound):
				writeError(w, http.StatusNotFound, "e-mail não encontrado")
			default:
				writeIMAPError(w, err, "erro ao buscar e-mail")
			}
			return
		}

		w.Header().Set("Content-Type", "message/rfc822")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fmt.Sprintf("%d.eml", uid)}))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}
}

// emptyFolderDeletedFlag é o item de STORE usado por handleEmptyFolder
// (e por deleteSpecificEmails) pra marcar mensagens como \Deleted antes
// do EXPUNGE. Silent: true porque a resposta (FLAGS atualizado de cada
// mensagem) não interessa aqui — só o efeito colateral no servidor.
var emptyFolderDeletedFlag = &imap.StoreFlags{
	Op:     imap.StoreFlagsAdd,
	Silent: true,
	Flags:  []imap.Flag{imap.FlagDeleted},
}

// trashFolderName é o nome fixo da pasta de lixeira usada por
// handleEmptyFolder (quando chamado com "uids" no body) — nome
// hardcoded, não descoberto via SPECIAL-USE (RFC 6154) nem informado
// por quem chama. Funciona pro Dovecot local (e qualquer conta cuja
// lixeira se chame exatamente "Trash") — contas reais com outro nome de
// lixeira (ex: "Lixeira", "Deleted Items") não são cobertas por este
// mecanismo.
const trashFolderName = "Trash"

// errTrashFolderMissing indica que handleEmptyFolder tentou mover
// mensagens pra trashFolderName, mas essa pasta não existe na conta.
var errTrashFolderMissing = errors.New("pasta de lixeira (\"" + trashFolderName + "\") não encontrada nesta conta")

// errDestinationFolderNotFound indica que handleMoveEmails tentou mover
// mensagens (campo "destinationFolder") pra uma pasta que não existe.
var errDestinationFolderNotFound = errors.New("pasta de destino não encontrada")

// deleteEmailsRequest é o body opcional de DELETE
// /folders/{pasta}/emails — omitido (ou "uids" vazio/ausente) preserva
// o comportamento de esvaziar a pasta inteira; com "uids" preenchido,
// apaga só essas mensagens (lixeira-aware, ver deleteSpecificEmails).
type deleteEmailsRequest struct {
	UIDs []uint32 `json:"uids"`
}

// handleEmptyFolder implementa DELETE /folders/{pasta}/emails com dois
// comportamentos, conforme o body:
//   - sem body (ou "uids" vazio/ausente): esvazia a pasta inteira —
//     marca \Deleted em todas as mensagens e faz EXPUNGE (sempre
//     definitivo, sem passar pela lixeira — mesmo comportamento de
//     sempre). Idempotente: pasta já vazia não é erro.
//   - com "uids" no body: apaga só essas mensagens especificamente,
//     lixeira-aware — move pra trashFolderName, a menos que pasta já
//     seja a própria lixeira, caso em que apaga definitivo (mesma
//     semântica de um cliente de e-mail: apagar de novo algo que já
//     está na lixeira apaga de vez).
//
// @Summary Esvazia uma pasta, ou apaga mensagens específicas dela
// @Description Sem body (ou "uids" vazio): esvazia a pasta inteira, sempre definitivo (marca \Deleted em todas e EXPUNGE) — a pasta em si continua existindo. Com "uids" no body: apaga só essas mensagens, lixeira-aware (move pra "Trash", ou apaga definitivo se a pasta já for "Trash"). Idempotente: pasta já vazia (ou uids que não existem mais) não é erro.
// @Tags e-mails
// @Accept json
// @Param pasta path string true "Nome da pasta (barra de nível aninhado como %2F)"
// @Param request body deleteEmailsRequest false "uids a apagar — omitido/vazio esvazia a pasta inteira"
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 204 "mensagens apagadas"
// @Failure 400 {object} errorResponse "nome da pasta ausente, ou body inválido"
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 404 {object} errorResponse "pasta não encontrada, ou (só com uids, movendo pra lixeira) pasta de lixeira não encontrada"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /folders/{pasta}/emails [delete]
func handleEmptyFolder(cfg config.IMAPConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pasta := r.PathValue("pasta")
		if pasta == "" {
			writeError(w, http.StatusBadRequest, "nome da pasta é obrigatório")
			return
		}

		var payload deleteEmailsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "body inválido: "+err.Error())
			return
		}

		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		err := imapx.WithClient(cfg, creds.user, creds.password, func(c *imapclient.Client) error {
			selectData, err := c.Select(pasta, nil).Wait()
			if err != nil {
				if isNonExistentMailbox(err) {
					return errFolderNotFound
				}
				return err
			}

			if len(payload.UIDs) > 0 {
				return deleteSpecificEmails(c, pasta, payload.UIDs)
			}

			if selectData.NumMessages == 0 {
				// STORE 1:* com a pasta vazia é erro (BAD "Invalid
				// messageset"), mesma situação do FETCH em
				// handleListEmails — nada a apagar, então nem tenta.
				return nil
			}

			var seqSet imap.SeqSet
			seqSet.AddRange(1, 0) // 1:* — todas as mensagens da pasta

			if _, err := c.Store(seqSet, emptyFolderDeletedFlag, nil).Collect(); err != nil {
				return err
			}

			return c.Expunge().Close()
		})

		if err != nil {
			switch {
			case errors.Is(err, errFolderNotFound):
				writeError(w, http.StatusNotFound, "pasta não encontrada")
			case errors.Is(err, errTrashFolderMissing):
				writeError(w, http.StatusNotFound, err.Error())
			default:
				writeIMAPError(w, err, "erro ao apagar e-mail(s)")
			}
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// deleteSpecificEmails apaga uids da pasta pasta (já selecionada em
// modo leitura-escrita) — lixeira-aware: se pasta já é trashFolderName,
// apaga definitivo (STORE \Deleted + UID EXPUNGE, escopado só a uids —
// diferente de um EXPUNGE comum, que apagaria qualquer outra mensagem
// que porventura já estivesse \Deleted na pasta por outro motivo); caso
// contrário, move pra trashFolderName (MOVE nativo do IMAP, com
// fallback automático pra COPY+STORE+EXPUNGE se o servidor não suportar
// a extensão — ver doc de Client.Move do go-imap).
func deleteSpecificEmails(c *imapclient.Client, pasta string, uids []uint32) error {
	var uidSet imap.UIDSet
	for _, uid := range uids {
		uidSet.AddNum(imap.UID(uid))
	}

	if pasta == trashFolderName {
		if _, err := c.Store(uidSet, emptyFolderDeletedFlag, nil).Collect(); err != nil {
			return err
		}
		return c.UIDExpunge(uidSet).Close()
	}

	if _, err := c.Move(uidSet, trashFolderName).Wait(); err != nil {
		if isTryCreate(err) {
			return errTrashFolderMissing
		}
		return err
	}
	return nil
}

// patchEmailFlagsRequest é o body de PATCH /folders/{pasta}/emails/flags
// — "uids" é obrigatório e não pode ser vazio; cada flag é opcional
// (*bool, nil = não mexe nela) — só altera a flag que vier como true ou
// false, ao menos uma precisa vir. Pensado pra crescer: uma flag nova
// (ex: draft) é só mais um campo *bool aqui e uma entrada em
// emailFlagChanges, sem mudar o shape das flags que já existem.
type patchEmailFlagsRequest struct {
	UIDs      []uint32 `json:"uids"`
	Read      *bool    `json:"read,omitempty"`
	Starred   *bool    `json:"starred,omitempty"`
	Answered  *bool    `json:"answered,omitempty"`
	Forwarded *bool    `json:"forwarded,omitempty"`
}

// hasAnyFlag reporta se ao menos uma flag foi informada no body — sem
// isso não há o que fazer com "uids".
func (p patchEmailFlagsRequest) hasAnyFlag() bool {
	return p.Read != nil || p.Starred != nil || p.Answered != nil || p.Forwarded != nil
}

// emailFlagChanges converte os campos informados de p pras mudanças de
// flag IMAP correspondentes, na mesma ordem dos campos do struct —
// mesmos nomes/flags que emailDetailFromMessage usa pra montar
// read/starred/answered/forwarded na leitura (GET .../emails/{uid}), só
// que na direção contrária (aqui é escrita).
func (p patchEmailFlagsRequest) emailFlagChanges() []struct {
	value *bool
	flag  imap.Flag
} {
	return []struct {
		value *bool
		flag  imap.Flag
	}{
		{p.Read, imap.FlagSeen},
		{p.Starred, imap.FlagFlagged},
		{p.Answered, imap.FlagAnswered},
		{p.Forwarded, imap.FlagForwarded},
	}
}

// handlePatchEmailFlags implementa PATCH /folders/{pasta}/emails/flags:
// adiciona/remove, em lote, as flags informadas (read/starred/answered/
// forwarded — cada uma opcional, só a que vier como true ou false é
// alterada) nas mensagens de "uids".
//
// @Summary Marca/desmarca flags de mensagens
// @Description Adiciona/remove, em lote, as flags informadas (read → \Seen, starred → \Flagged, answered → \Answered, forwarded → $Forwarded) sobre as mensagens de "uids" — todas opcionais, mas ao menos uma precisa vir; só a(s) informada(s) é(são) alterada(s), as demais ficam como estavam.
// @Tags e-mails
// @Accept json
// @Param pasta path string true "Nome da pasta (barra de nível aninhado como %2F)"
// @Param request body patchEmailFlagsRequest true "uids a alterar, mais ao menos uma flag"
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 204 "flags alteradas"
// @Failure 400 {object} errorResponse "nome da pasta ausente, body inválido, uids vazio, ou nenhuma flag informada"
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 404 {object} errorResponse "pasta não encontrada"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /folders/{pasta}/emails/flags [patch]
func handlePatchEmailFlags(cfg config.IMAPConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pasta := r.PathValue("pasta")
		if pasta == "" {
			writeError(w, http.StatusBadRequest, "nome da pasta é obrigatório")
			return
		}

		var payload patchEmailFlagsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "body inválido: "+err.Error())
			return
		}
		if len(payload.UIDs) == 0 {
			writeError(w, http.StatusBadRequest, "uids é obrigatório e não pode ser vazio")
			return
		}
		if !payload.hasAnyFlag() {
			writeError(w, http.StatusBadRequest, "informe ao menos uma flag (read, starred, answered ou forwarded)")
			return
		}

		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		var uidSet imap.UIDSet
		for _, uid := range payload.UIDs {
			uidSet.AddNum(imap.UID(uid))
		}

		err := imapx.WithClient(cfg, creds.user, creds.password, func(c *imapclient.Client) error {
			if _, err := c.Select(pasta, nil).Wait(); err != nil {
				if isNonExistentMailbox(err) {
					return errFolderNotFound
				}
				return err
			}

			for _, change := range payload.emailFlagChanges() {
				if change.value == nil {
					continue
				}
				if err := storeFlag(c, uidSet, change.flag, *change.value); err != nil {
					return err
				}
			}

			return nil
		})

		if err != nil {
			if errors.Is(err, errFolderNotFound) {
				writeError(w, http.StatusNotFound, "pasta não encontrada")
				return
			}
			writeIMAPError(w, err, "erro ao alterar flags")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// storeFlag adiciona (enable) ou remove (!enable) flag nas mensagens de
// uidSet, na pasta já selecionada. Silent: true porque a resposta
// (FLAGS atualizado de cada mensagem) não interessa aqui — só o efeito
// colateral no servidor.
func storeFlag(c *imapclient.Client, uidSet imap.UIDSet, flag imap.Flag, enable bool) error {
	op := imap.StoreFlagsDel
	if enable {
		op = imap.StoreFlagsAdd
	}
	store := &imap.StoreFlags{Op: op, Silent: true, Flags: []imap.Flag{flag}}
	_, err := c.Store(uidSet, store, nil).Collect()
	return err
}

// moveEmailsRequest é o body de PATCH /folders/{pasta}/emails/move —
// "uids" e "destinationFolder" são obrigatórios.
type moveEmailsRequest struct {
	UIDs              []uint32 `json:"uids"`
	DestinationFolder string   `json:"destinationFolder"`
}

// handleMoveEmails implementa PATCH /folders/{pasta}/emails/move: move,
// em lote, as mensagens de "uids" da pasta pedida pra
// "destinationFolder".
//
// @Summary Move mensagens pra outra pasta
// @Description Move, em lote, as mensagens de "uids" da pasta {pasta} pra "destinationFolder".
// @Tags e-mails
// @Accept json
// @Param pasta path string true "Nome da pasta de origem (barra de nível aninhado como %2F)"
// @Param request body moveEmailsRequest true "uids a mover, mais destinationFolder"
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 204 "mensagens movidas"
// @Failure 400 {object} errorResponse "nome da pasta ausente, body inválido, uids vazio, ou destinationFolder ausente"
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 404 {object} errorResponse "pasta de origem ou de destino não encontrada"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /folders/{pasta}/emails/move [patch]
func handleMoveEmails(cfg config.IMAPConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pasta := r.PathValue("pasta")
		if pasta == "" {
			writeError(w, http.StatusBadRequest, "nome da pasta é obrigatório")
			return
		}

		var payload moveEmailsRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "body inválido: "+err.Error())
			return
		}
		if len(payload.UIDs) == 0 {
			writeError(w, http.StatusBadRequest, "uids é obrigatório e não pode ser vazio")
			return
		}
		if payload.DestinationFolder == "" {
			writeError(w, http.StatusBadRequest, "destinationFolder é obrigatório")
			return
		}

		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		var uidSet imap.UIDSet
		for _, uid := range payload.UIDs {
			uidSet.AddNum(imap.UID(uid))
		}

		err := imapx.WithClient(cfg, creds.user, creds.password, func(c *imapclient.Client) error {
			if _, err := c.Select(pasta, nil).Wait(); err != nil {
				if isNonExistentMailbox(err) {
					return errFolderNotFound
				}
				return err
			}

			if _, err := c.Move(uidSet, payload.DestinationFolder).Wait(); err != nil {
				if isTryCreate(err) {
					return errDestinationFolderNotFound
				}
				return err
			}
			return nil
		})

		if err != nil {
			switch {
			case errors.Is(err, errFolderNotFound):
				writeError(w, http.StatusNotFound, "pasta não encontrada")
			case errors.Is(err, errDestinationFolderNotFound):
				writeError(w, http.StatusNotFound, err.Error())
			default:
				writeIMAPError(w, err, "erro ao mover e-mail(s)")
			}
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// paginationWindow calcula os índices [start, end) da página pedida
// sobre uma lista já ordenada de total itens, e o total de páginas —
// compartilhado por paginate (GET /folders/{pasta}/emails) e
// paginateSearch (GET /emails/search).
func paginationWindow(total int, params paginationParams) (start, end, totalPages int) {
	if total > 0 {
		totalPages = (total + params.pageSize - 1) / params.pageSize
	}

	start = (params.page - 1) * params.pageSize
	end = start + params.pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	return start, end, totalPages
}

// paginate recorta messages (já ordenada) pra página pedida e monta o
// body de resposta com os metadados de paginação.
func paginate(messages []*imapclient.FetchMessageBuffer, params listEmailsParams) emailsResponse {
	total := len(messages)
	start, end, totalPages := paginationWindow(total, params.paginationParams)

	page := messages[start:end]
	emails := make([]emailInfo, 0, len(page))
	for _, msg := range page {
		emails = append(emails, emailInfoFromMessage(msg))
	}

	return emailsResponse{
		Emails:     emails,
		Page:       params.page,
		PageSize:   params.pageSize,
		Total:      total,
		TotalPages: totalPages,
	}
}

// isNonExistentMailbox reporta se err é a resposta IMAP de "pasta não
// existe" (RFC 5530, response code NONEXISTENT) — devolvida pelo
// Dovecot num SELECT/EXAMINE de uma pasta inexistente.
func isNonExistentMailbox(err error) bool {
	var imapErr *imap.Error
	return errors.As(err, &imapErr) && imapErr.Code == imap.ResponseCodeNonExistent
}

// isAlreadyExists reporta se err é a resposta IMAP de "pasta já existe"
// (RFC 5530, response code ALREADYEXISTS) — devolvida por um CREATE ou
// RENAME cujo nome de destino já é usado por outra pasta.
func isAlreadyExists(err error) bool {
	var imapErr *imap.Error
	return errors.As(err, &imapErr) && imapErr.Code == imap.ResponseCodeAlreadyExists
}

// isTryCreate reporta se err é a resposta IMAP de "pasta de destino não
// existe" (RFC 3501, response code TRYCREATE) — devolvida por um
// COPY/MOVE (e o COPY interno do fallback de MOVE) cuja pasta de
// destino não existe.
func isTryCreate(err error) bool {
	var imapErr *imap.Error
	return errors.As(err, &imapErr) && imapErr.Code == imap.ResponseCodeTryCreate
}

// envelopeDate devolve a data da mensagem, ou o zero-value de
// time.Time se a mensagem não tiver ENVELOPE (mensagem malformada) —
// nesse caso ela cai pro fim da lista ordenada por data.
func envelopeDate(msg *imapclient.FetchMessageBuffer) time.Time {
	if msg.Envelope == nil {
		return time.Time{}
	}
	return msg.Envelope.Date
}

// emailInfoFromMessage converte os dados de FETCH pro shape JSON do
// endpoint.
func emailInfoFromMessage(msg *imapclient.FetchMessageBuffer) emailInfo {
	info := emailInfo{UID: uint32(msg.UID)}

	if msg.Envelope != nil {
		info.Subject = msg.Envelope.Subject
		info.From = formatFrom(msg.Envelope.From)
	}

	if msg.BodyStructure != nil {
		info.HasAttachment = hasAttachment(msg.BodyStructure)
	}

	for _, flag := range msg.Flags {
		if flag == imap.FlagForwarded {
			info.Forwarded = true
			break
		}
	}

	return info
}

// formatFrom devolve o nome de exibição do primeiro remetente, ou o
// e-mail se não houver nome — igual ao que a maioria dos clientes de
// e-mail mostra numa lista de mensagens. String vazia se não houver
// remetente (ENVELOPE malformado).
func formatFrom(addrs []imap.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	addr := addrs[0]
	if addr.Name != "" {
		return addr.Name
	}
	return addr.Addr()
}

// hasAttachment percorre o BODYSTRUCTURE em busca de uma parte com
// Content-Disposition: attachment — imagens/arquivos inline (Content-
// Disposition: inline, ex: logo embutido no corpo HTML) não contam.
func hasAttachment(bs imap.BodyStructure) bool {
	found := false
	bs.Walk(func(path []int, part imap.BodyStructure) bool {
		if d := part.Disposition(); d != nil && strings.EqualFold(d.Value, "attachment") {
			found = true
		}
		return true
	})
	return found
}

// emailDetailFromMessage converte os dados de FETCH (ENVELOPE, FLAGS e
// o BODY[] pedido em emailBodySection) pro shape JSON de GET
// /folders/{pasta}/emails/{uid}.
func emailDetailFromMessage(msg *imapclient.FetchMessageBuffer) emailDetail {
	detail := emailDetail{UID: uint32(msg.UID)}

	if msg.Envelope != nil {
		detail.Subject = msg.Envelope.Subject
		detail.Date = msg.Envelope.Date
		if len(msg.Envelope.From) > 0 {
			detail.From = emailAddressFromAddr(msg.Envelope.From[0])
		}
		detail.To = emailAddressesFromAddrs(msg.Envelope.To)
		detail.Cc = emailAddressesFromAddrs(msg.Envelope.Cc)
	}

	for _, flag := range msg.Flags {
		switch flag {
		case imap.FlagSeen:
			detail.Read = true
		case imap.FlagFlagged:
			detail.Starred = true
		case imap.FlagAnswered:
			detail.Answered = true
		case imap.FlagForwarded:
			detail.Forwarded = true
		}
	}

	detail.MimeType, detail.Body, detail.Attachments = parseEmailBody(msg.FindBodySection(emailBodySection))

	return detail
}

// emailAddressFromAddr converte um imap.Address pro shape JSON da API.
func emailAddressFromAddr(addr imap.Address) emailAddress {
	return emailAddress{Name: addr.Name, Email: addr.Addr()}
}

// emailAddressesFromAddrs converte uma lista de imap.Address pro shape
// JSON da API.
func emailAddressesFromAddrs(addrs []imap.Address) []emailAddress {
	out := make([]emailAddress, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, emailAddressFromAddr(addr))
	}
	return out
}

// parseEmailBody parseia o RFC 822 bruto de raw (via go-message/mail)
// e devolve o corpo de exibição — HTML tem preferência sobre texto
// simples quando a mensagem tem os dois (multipart/alternative), como
// a maioria dos clientes de e-mail faz — mais a lista leve de anexos
// (Content-Disposition: attachment; sem o conteúdo binário). Partes
// inline não-texto (ex: imagem embutida no corpo HTML) não contam
// como anexo, mesma regra de hasAttachment.
//
// Nunca falha: o corpo de um e-mail real é fundamentalmente "best
// effort" — mensagens reais (ex: o corpus do SpamAssassin usado nos
// fixtures de teste) aparecem com toda sorte de malformação no
// multipart (boundary ausente/vazio, mensagem truncada no meio,
// Content-Transfer-Encoding desconhecido, base64 inválido, header
// quebrado...), cada uma com sua própria mensagem de erro específica
// do go-message, sem um jeito confiável de enumerá-las todas. Em vez
// de tentar prever cada uma, qualquer erro de parsing simplesmente
// para de acrescentar conteúdo além do que já foi lido — os metadados
// (ENVELOPE/FLAGS, já obtidos antes de chamar esta função) continuam
// valendo, e a requisição não falha por causa do corpo.
func parseEmailBody(raw []byte) (mimeType, body string, attachments []attachmentInfo) {
	attachments = make([]attachmentInfo, 0)

	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil && !isRecoverableParseError(err) {
		// go-message/mail nem devolve um Reader utilizável nesse caso
		// (ex: header RFC 822 quebrado demais pra nem começar a
		// parsear) — sem corpo/anexos, mas ainda assim 200 com os
		// metadados.
		return "", "", attachments
	}

	var htmlBody, plainBody string
	var haveHTML, havePlain bool

	for {
		part, err := r.NextPart()
		if err != nil {
			// Cobre o fim normal das partes (io.EOF) e qualquer forma
			// de multipart que o go-message não consiga continuar
			// lendo — não é fatal, só para de acrescentar partes.
			break
		}
		if part == nil {
			// Limitação do go-message/mail: apesar do que a doc de
			// NextPart promete, um erro de Content-Transfer-Encoding
			// desconhecido numa parte aninhada de um multipart (ex:
			// "Content-Transfer-Encoding: as-is", visto em fixtures
			// reais do corpus do SpamAssassin) faz o Reader devolver
			// só o erro, sem a Part utilizável — pula essa parte em
			// vez de tentar usá-la.
			continue
		}

		switch h := part.Header.(type) {
		case *mail.InlineHeader:
			contentType, _, _ := h.ContentType()
			// io.ReadAll sempre devolve o que já leu junto de um
			// eventual erro (ex: io.ErrUnexpectedEOF num corpo
			// truncado) — usa o que der, em vez de descartar tudo.
			b, _ := io.ReadAll(part.Body)
			switch contentType {
			case "text/html":
				htmlBody, haveHTML = string(b), true
			case "text/plain":
				if !havePlain {
					plainBody, havePlain = string(b), true
				}
			}
		case *mail.AttachmentHeader:
			filename, _ := h.Filename()
			contentType, _, _ := h.ContentType()
			b, _ := io.ReadAll(part.Body)
			attachments = append(attachments, attachmentInfo{
				Filename:    filename,
				ContentType: contentType,
				Size:        int64(len(b)),
			})
		}
	}

	switch {
	case haveHTML:
		return "text/html", htmlBody, attachments
	case havePlain:
		return "text/plain", plainBody, attachments
	default:
		return "", "", attachments
	}
}

// isRecoverableParseError reporta se err é um erro de charset ou
// Content-Transfer-Encoding desconhecidos — nesses casos o
// go-message/mail ainda devolve um Reader utilizável (com o corpo
// bruto, não decodificado), diferente de outros erros de parsing (ex:
// header malformado) onde o Reader vem nil.
func isRecoverableParseError(err error) bool {
	return message.IsUnknownCharset(err) || message.IsUnknownEncoding(err)
}
