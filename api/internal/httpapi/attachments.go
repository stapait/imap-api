package httpapi

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"

	"imap-api/internal/config"
	"imap-api/internal/imapx"
)

// errAttachmentNotFound indica que o {attachmentId} pedido em GET
// /folders/{pasta}/emails/{uid}/attachments/{attachmentId} não
// corresponde a nenhum anexo da mensagem — id mal formado (não é um dos
// que a própria API gerou em attachmentLink), índice fora do alcance
// (mensagem tem menos anexos que isso), ou a mensagem não tem parsing
// utilizável (ver isRecoverableParseError).
var errAttachmentNotFound = errors.New("anexo não encontrado")

// attachmentID monta o id sintético de um anexo — base64 (URL-safe, sem
// padding, então cabe inteiro num segmento de path sem precisar de
// percent-encoding) de "índice:nome do arquivo". O índice é a única
// parte realmente usada pra localizar o anexo de volta (ver
// parseAttachmentID/findAttachment) — nome de anexo não é garantido
// único dentro de uma mensagem, nem sempre presente (mensagem
// malformada pode ter uma parte "attachment" sem filename nenhum), veio
// só pra deixar o id "legível" (dá pra reconhecer do que se trata só de
// olhar, mesmo sendo opaco). O nome embutido no id não é usado de volta
// em nenhuma lógica — só o índice.
func attachmentID(index int, filename string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%s", index, filename)))
}

// parseAttachmentID decodifica um id montado por attachmentID e devolve
// o índice do anexo. ok é false pra qualquer id que não tenha vindo de
// attachmentID (base64 inválido, ou sem o "índice:" na frente) — tratado
// pelo handler como 404 "anexo não encontrado", já que é um id opaco
// (não é um parâmetro que um cliente monta à mão, sempre vem de
// attachmentLink).
func parseAttachmentID(id string) (index int, ok bool) {
	raw, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return 0, false
	}

	indexPart, _, found := strings.Cut(string(raw), ":")
	if !found {
		return 0, false
	}

	n, err := strconv.Atoi(indexPart)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// attachmentLink monta o path (relativo, sem host) de GET
// .../attachments/{attachmentId} pro anexo de índice index da mensagem
// uid, na pasta pasta — devolvido pronto em attachmentInfo.AttachmentLink
// (ver handleGetEmail). pasta é percent-encoded como em qualquer outro
// link desta API (barra de pasta aninhada vira %2F).
func attachmentLink(pasta string, uid uint64, index int, filename string) string {
	return fmt.Sprintf("/folders/%s/emails/%d/attachments/%s", url.PathEscape(pasta), uid, attachmentID(index, filename))
}

// handleGetAttachment implementa GET
// /folders/{pasta}/emails/{uid}/attachments/{attachmentId}: devolve o
// conteúdo binário do anexo de índice attachmentId (ver
// attachmentID/parseAttachmentID) da mensagem. Select em modo leitura
// (EXAMINE) — diferente de GET .../emails/{uid}, baixar um anexo não
// marca a mensagem como lida.
//
// @Summary Baixa um anexo
// @Description Conteúdo binário de um anexo específico (Content-Type do anexo, Content-Disposition: attachment) — attachmentId vem pronto no campo attachmentLink de cada item de "attachments" em GET .../emails/{uid}, não é pra ser montado à mão. Diferente da leitura do e-mail completo, não marca a mensagem como lida.
// @Tags anexos
// @Produce application/octet-stream
// @Param pasta path string true "Nome da pasta (barra de nível aninhado como %2F)"
// @Param uid path int true "UID da mensagem (IMAP)"
// @Param attachmentId path string true "Id do anexo (vem de attachmentLink em GET .../emails/{uid})"
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 200 {file} file
// @Failure 400 {object} errorResponse "nome da pasta ausente, ou uid não numérico"
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 404 {object} errorResponse "pasta, e-mail ou anexo não encontrados"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /folders/{pasta}/emails/{uid}/attachments/{attachmentId} [get]
func handleGetAttachment(cfg config.IMAPConfig) http.HandlerFunc {
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

		index, ok := parseAttachmentID(r.PathValue("attachmentId"))
		if !ok {
			writeError(w, http.StatusNotFound, "anexo não encontrado")
			return
		}

		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		var info attachmentInfo
		var data []byte
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

			var found bool
			info, data, found = findAttachment(messages[0].FindBodySection(emailBodySection), index)
			if !found {
				return errAttachmentNotFound
			}
			return nil
		})

		if err != nil {
			switch {
			case errors.Is(err, errFolderNotFound):
				writeError(w, http.StatusNotFound, "pasta não encontrada")
			case errors.Is(err, errEmailNotFound), errors.Is(err, errAttachmentNotFound):
				writeError(w, http.StatusNotFound, "anexo não encontrado")
			default:
				writeIMAPError(w, err, "erro ao buscar anexo")
			}
			return
		}

		contentType := info.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Filename}))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

// findAttachment percorre o e-mail bruto (mesmo parsing de
// parseEmailBody, go-message/mail, mesma ordem de partes) em busca do
// anexo de índice index — o mesmo índice que parseEmailBody atribuiria
// implicitamente à posição dele dentro do slice attachments que monta.
// found é false se o parsing não render um Reader utilizável, ou se a
// mensagem não tiver esse índice (menos anexos que isso) — sem
// distinguir os dois motivos, mesma tolerância a mensagem malformada
// de parseEmailBody (ver comentário lá): não é a busca que deveria
// decidir se um parsing malformado é "erro" ou não.
func findAttachment(raw []byte, index int) (info attachmentInfo, data []byte, found bool) {
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil && !isRecoverableParseError(err) {
		return attachmentInfo{}, nil, false
	}

	current := 0
	for {
		part, err := r.NextPart()
		if err != nil {
			return attachmentInfo{}, nil, false
		}
		if part == nil {
			continue
		}

		h, isAttachment := part.Header.(*mail.AttachmentHeader)
		if !isAttachment {
			continue
		}

		if current != index {
			current++
			continue
		}

		filename, _ := h.Filename()
		contentType, _, _ := h.ContentType()
		b, _ := io.ReadAll(part.Body)
		return attachmentInfo{Filename: filename, ContentType: contentType, Size: int64(len(b))}, b, true
	}
}
