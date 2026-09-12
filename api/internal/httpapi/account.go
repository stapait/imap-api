package httpapi

import (
	"log"
	"net/http"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"imap-api/internal/config"
	"imap-api/internal/imapx"
)

// accountResponse é o body de GET /account. QuotaTotal/QuotaUsed são
// ponteiros pra poder devolver null (em vez de 0) quando a conta/
// servidor não tem quota — 0 significaria "quota esgotada", null
// significa "não sabemos/não se aplica".
type accountResponse struct {
	QuotaTotal *int64 `json:"quotaTotal"`
	QuotaUsed  *int64 `json:"quotaUsed"`
}

// handleGetAccount implementa GET /account: quota de armazenamento
// (STORAGE) da conta autenticada, em bytes. O IMAP (GETQUOTAROOT, RFC
// 9208) devolve os valores em KiB (múltiplos de 1024 bytes) — convertido
// aqui pra bytes, mais direto de consumir. null em quotaTotal/quotaUsed
// se a conta não tiver quota de STORAGE configurada, ou se o servidor
// não suportar a extensão QUOTA — tratado como ausência de dado, não
// como erro (a requisição não falha por isso).
//
// @Summary Quota da conta
// @Description Quota de armazenamento (STORAGE) da conta autenticada, em bytes (total e usado) — o IMAP devolve em KiB, convertido aqui. null nos dois campos se a conta/servidor não tiver quota de STORAGE (não é erro).
// @Tags conta
// @Produce json
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 200 {object} accountResponse
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /account [get]
func handleGetAccount(cfg config.IMAPConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		var resp accountResponse
		err := imapx.WithClient(cfg, creds.user, creds.password, func(c *imapclient.Client) error {
			roots, err := c.GetQuotaRoot("INBOX").Wait()
			if err != nil {
				// Extensão QUOTA não suportada pelo servidor, ou outra
				// falha ao buscá-la — degrada pra "sem quota conhecida"
				// em vez de derrubar a requisição inteira por causa de
				// um dado auxiliar.
				log.Printf("erro ao buscar quota (degradando pra null): %v", err)
				return nil
			}

			for _, root := range roots {
				res, ok := root.Resources[imap.QuotaResourceStorage]
				if !ok {
					continue
				}
				total := res.Limit * 1024
				used := res.Usage * 1024
				resp.QuotaTotal = &total
				resp.QuotaUsed = &used
				return nil
			}
			return nil
		})

		if err != nil {
			writeIMAPError(w, err, "erro ao buscar conta")
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}
