package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"imap-api/internal/config"
	"imap-api/internal/imapx"
)

type folderInfo struct {
	Name     string `json:"name"`
	Messages uint32 `json:"messages"`
	Unread   uint32 `json:"unread"`
}

type foldersResponse struct {
	Folders []folderInfo `json:"folders"`
}

// errFolderNotFound indica que a pasta pedida em GET /folders/{pasta}
// não existe na conta autenticada.
var errFolderNotFound = errors.New("pasta não encontrada")

// errCannotDeleteInbox indica que DELETE /folders/{pasta} foi pedido
// pra INBOX — nunca permitido (RFC 3501).
var errCannotDeleteInbox = errors.New("não é possível apagar a INBOX")

// errCannotRenameInbox indica que PATCH /folders/{pasta} (rename) foi
// pedido pra INBOX. Não é uma proibição do protocolo como
// errCannotDeleteInbox (RFC 3501 até define semântica pra "RENAME
// INBOX": não renomeia a INBOX em si, só cria a pasta nova e move as
// mensagens pra lá, deixando a INBOX vazia) — mas essa semântica é
// surpreendente o bastante (não é o que "renomear" normalmente
// significa) que preferimos bloquear em vez de expor.
var errCannotRenameInbox = errors.New("não é possível renomear a INBOX")

// errFolderAlreadyExists indica que POST /folders/{pasta} (criar) ou
// PATCH /folders/{pasta} (renomear) resultaria numa pasta com nome que
// já existe.
var errFolderAlreadyExists = errors.New("pasta já existe")

// folderStatusOptions são os itens de STATUS pedidos em todo LIST desta
// API — contagem de mensagens e de não lidas (sem flag \Seen).
var folderStatusOptions = &imap.ListOptions{
	ReturnStatus: &imap.StatusOptions{
		NumMessages: true,
		NumUnseen:   true,
	},
}

// handleListFolders implementa GET /folders: lista as pastas da conta
// IMAP autenticada nos headers imapUser/imapPassword, com a contagem de
// mensagens e de não lidas (sem flag \Seen) de cada uma.
//
// @Summary Lista as pastas
// @Description Lista as pastas IMAP disponíveis na conta autenticada, com nome, total de mensagens e não lidas (contagem sem a flag \Seen, via LIST-STATUS) de cada uma.
// @Tags pastas
// @Produce json
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 200 {object} foldersResponse
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /folders [get]
func handleListFolders(cfg config.IMAPConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		var folders []folderInfo
		err := imapx.WithClient(cfg, creds.user, creds.password, func(c *imapclient.Client) error {
			// LIST-STATUS (RFC 5819): pega nome + contagens de todas as
			// pastas em uma única ida-e-volta, em vez de um STATUS por
			// pasta.
			mailboxes, err := c.List("", "*", folderStatusOptions).Collect()
			if err != nil {
				return err
			}

			folders = folderInfosFromListData(mailboxes)
			return nil
		})

		if err != nil {
			writeIMAPError(w, err, "erro ao listar pastas")
			return
		}

		writeJSON(w, http.StatusOK, foldersResponse{Folders: folders})
	}
}

// handleGetFolder implementa GET /folders/{pasta}: mesmo body de
// handleListFolders, mas restrito à pasta pedida e suas sub-pastas (se
// houver) — não inclui as demais pastas da conta.
//
// @Summary Busca uma pasta específica
// @Description Mesmo shape de GET /folders, restrito à pasta pedida e suas sub-pastas (diretas ou não) — não inclui as demais pastas da conta. Uma pasta aninhada é passada com a barra hierárquica percent-encoded (%2F), ex: "Trabalho%2FProjetos" pra "Trabalho/Projetos" — a barra literal não é aceita (vira mais um segmento de path e dá 404).
// @Tags pastas
// @Produce json
// @Param pasta path string true "Nome da pasta (barra de nível aninhado como %2F)"
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 200 {object} foldersResponse
// @Failure 400 {object} errorResponse "nome da pasta ausente"
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 404 {object} errorResponse "pasta não encontrada"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /folders/{pasta} [get]
func handleGetFolder(cfg config.IMAPConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pasta := r.PathValue("pasta")
		if pasta == "" {
			writeError(w, http.StatusBadRequest, "nome da pasta é obrigatório")
			return
		}

		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		var folders []folderInfo
		err := imapx.WithClient(cfg, creds.user, creds.password, func(c *imapclient.Client) error {
			// Primeiro LIST: a pasta em si (existência + delimitador
			// hierárquico usado pelo servidor, pra montar o pattern das
			// sub-pastas a seguir).
			root, err := c.List("", pasta, folderStatusOptions).Collect()
			if err != nil {
				return err
			}
			if len(root) == 0 {
				return errFolderNotFound
			}

			folders = folderInfosFromListData(root)

			// Segundo LIST: as sub-pastas diretas e indiretas de pasta,
			// se o servidor reportou um delimitador hierárquico pra ela
			// (mbox.Delim == 0 significa que ela não pode ter filhas).
			if delim := root[0].Delim; delim != 0 {
				children, err := c.List("", pasta+string(delim)+"*", folderStatusOptions).Collect()
				if err != nil {
					return err
				}
				folders = append(folders, folderInfosFromListData(children)...)
			}

			return nil
		})

		if err != nil {
			if errors.Is(err, errFolderNotFound) {
				writeError(w, http.StatusNotFound, "pasta não encontrada")
				return
			}
			writeIMAPError(w, err, "erro ao buscar pasta")
			return
		}

		writeJSON(w, http.StatusOK, foldersResponse{Folders: folders})
	}
}

// handleDeleteFolder implementa DELETE /folders/{pasta}: apaga a pasta
// pedida e todas as suas sub-pastas, recursivamente.
//
// @Summary Apaga uma pasta
// @Description Apaga a pasta pedida e todas as suas sub-pastas (diretas ou não), recursivamente. INBOX nunca pode ser apagada (regra do próprio protocolo IMAP).
// @Tags pastas
// @Param pasta path string true "Nome da pasta (barra de nível aninhado como %2F)"
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 204 "pasta apagada"
// @Failure 400 {object} errorResponse "nome da pasta ausente, ou é a INBOX (nunca pode ser apagada)"
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 404 {object} errorResponse "pasta não encontrada"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /folders/{pasta} [delete]
func handleDeleteFolder(cfg config.IMAPConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pasta := r.PathValue("pasta")
		if pasta == "" {
			writeError(w, http.StatusBadRequest, "nome da pasta é obrigatório")
			return
		}

		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		err := imapx.WithClient(cfg, creds.user, creds.password, func(c *imapclient.Client) error {
			if strings.EqualFold(pasta, "INBOX") {
				// RFC 3501: INBOX nunca pode ser apagada (é a única
				// pasta que sempre existe para todo usuário) — regra do
				// protocolo IMAP em si, válida em qualquer servidor, não
				// uma particularidade do Dovecot. Checado só depois do
				// login (dentro do WithClient) pra credenciais inválidas
				// sempre darem 401, mesmo pedindo pra apagar a INBOX —
				// em vez de traduzir o "NO" que o servidor devolveria
				// (sem response code estruturado, só texto livre) num
				// 502 genérico e enganoso.
				return errCannotDeleteInbox
			}

			// Primeiro LIST: a pasta em si (existência + delimitador
			// hierárquico, pra montar o pattern das sub-pastas a seguir)
			// — sem STATUS, que não é preciso pra apagar.
			root, err := c.List("", pasta, nil).Collect()
			if err != nil {
				return err
			}
			if len(root) == 0 {
				return errFolderNotFound
			}

			names := []string{pasta}
			if delim := root[0].Delim; delim != 0 {
				children, err := c.List("", pasta+string(delim)+"*", nil).Collect()
				if err != nil {
					return err
				}
				for _, child := range children {
					names = append(names, child.Mailbox)
				}
			}

			// Apaga da sub-pasta mais aninhada até a pasta pedida por
			// último — servidores IMAP podem recusar apagar uma pasta
			// que ainda tem sub-pastas. Ordenar pelo comprimento do nome
			// (decrescente) já garante essa ordem: o nome de uma
			// sub-pasta sempre tem o nome do pai como prefixo, logo é
			// sempre mais longo — mesmo comparando ramos diferentes da
			// árvore, um nó nunca é mais curto que qualquer um dos seus
			// próprios ancestrais.
			sort.Slice(names, func(i, j int) bool {
				return len(names[i]) > len(names[j])
			})

			for _, name := range names {
				if err := c.Delete(name).Wait(); err != nil {
					return err
				}
			}
			return nil
		})

		if err != nil {
			switch {
			case errors.Is(err, errCannotDeleteInbox):
				writeError(w, http.StatusBadRequest, err.Error())
			case errors.Is(err, errFolderNotFound):
				writeError(w, http.StatusNotFound, "pasta não encontrada")
			default:
				writeIMAPError(w, err, "erro ao apagar pasta")
			}
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleCreateFolder implementa POST /folders/{pasta}: cria a pasta
// pedida. Sem validação própria de nome (tamanho, caracteres) — só
// repassa pro CREATE do IMAP e traduz o erro real do servidor,
// consistente com o resto da API (que também não reimplementa
// validação que o próprio protocolo já garante).
//
// @Summary Cria uma pasta
// @Description Cria a pasta pedida (barra de nível aninhado como %2F pra criar direto uma sub-pasta, ex: "Trabalho%2FNovaPasta"). Sem validação própria de nome — erros de nome inválido/pasta já existente vêm do próprio servidor IMAP.
// @Tags pastas
// @Produce json
// @Param pasta path string true "Nome da pasta a criar (barra de nível aninhado como %2F)"
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 201 {object} folderInfo
// @Failure 400 {object} errorResponse "nome da pasta ausente"
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 409 {object} errorResponse "pasta já existe"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP (inclui nome de pasta rejeitado pelo servidor)"
// @Router /folders/{pasta} [post]
func handleCreateFolder(cfg config.IMAPConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pasta := r.PathValue("pasta")
		if pasta == "" {
			writeError(w, http.StatusBadRequest, "nome da pasta é obrigatório")
			return
		}

		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		err := imapx.WithClient(cfg, creds.user, creds.password, func(c *imapclient.Client) error {
			if err := c.Create(pasta, nil).Wait(); err != nil {
				if isAlreadyExists(err) {
					return errFolderAlreadyExists
				}
				return err
			}
			return nil
		})

		if err != nil {
			switch {
			case errors.Is(err, errFolderAlreadyExists):
				writeError(w, http.StatusConflict, err.Error())
			default:
				writeIMAPError(w, err, "erro ao criar pasta")
			}
			return
		}

		// Pasta recém-criada: sempre vazia, sem precisar de um LIST-STATUS
		// extra pra saber disso.
		writeJSON(w, http.StatusCreated, folderInfo{Name: pasta})
	}
}

// renameFolderRequest é o body de PATCH /folders/{pasta}.
type renameFolderRequest struct {
	Name string `json:"name"`
}

// handleRenameFolder implementa PATCH /folders/{pasta}: renomeia a
// pasta pedida pro nome em Name. Mesma filosofia de handleCreateFolder
// — sem validação própria de nome, só repassa pro RENAME do IMAP.
//
// @Summary Renomeia uma pasta
// @Description Renomeia a pasta pedida — body {"name": "NovoNome"}. INBOX não pode ser renomeada (RFC 3501 define uma semântica especial pra "RENAME INBOX" — não renomeia a INBOX em si, só move as mensagens pra uma pasta nova — surpreendente demais pra expor, por isso bloqueada). Sem validação própria de nome — erros de nome inválido/pasta já existente vêm do próprio servidor IMAP.
// @Tags pastas
// @Accept json
// @Produce json
// @Param pasta path string true "Nome atual da pasta (barra de nível aninhado como %2F)"
// @Param request body renameFolderRequest true "Novo nome"
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 200 {object} foldersResponse
// @Failure 400 {object} errorResponse "nome da pasta ausente, body inválido, name ausente, ou é a INBOX"
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 404 {object} errorResponse "pasta não encontrada"
// @Failure 409 {object} errorResponse "já existe uma pasta com o novo nome"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /folders/{pasta} [patch]
func handleRenameFolder(cfg config.IMAPConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pasta := r.PathValue("pasta")
		if pasta == "" {
			writeError(w, http.StatusBadRequest, "nome da pasta é obrigatório")
			return
		}

		var payload renameFolderRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "body inválido: "+err.Error())
			return
		}
		if payload.Name == "" {
			writeError(w, http.StatusBadRequest, "name é obrigatório")
			return
		}

		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		var folders []folderInfo
		err := imapx.WithClient(cfg, creds.user, creds.password, func(c *imapclient.Client) error {
			if strings.EqualFold(pasta, "INBOX") {
				return errCannotRenameInbox
			}

			if err := c.Rename(pasta, payload.Name, nil).Wait(); err != nil {
				if isNonExistentMailbox(err) {
					return errFolderNotFound
				}
				if isAlreadyExists(err) {
					return errFolderAlreadyExists
				}
				return err
			}

			mailboxes, err := c.List("", payload.Name, folderStatusOptions).Collect()
			if err != nil {
				return err
			}
			folders = folderInfosFromListData(mailboxes)
			return nil
		})

		if err != nil {
			switch {
			case errors.Is(err, errCannotRenameInbox):
				writeError(w, http.StatusBadRequest, err.Error())
			case errors.Is(err, errFolderNotFound):
				writeError(w, http.StatusNotFound, "pasta não encontrada")
			case errors.Is(err, errFolderAlreadyExists):
				writeError(w, http.StatusConflict, err.Error())
			default:
				writeIMAPError(w, err, "erro ao renomear pasta")
			}
			return
		}

		writeJSON(w, http.StatusOK, foldersResponse{Folders: folders})
	}
}

// folderInfosFromListData converte as respostas de LIST-STATUS do
// go-imap pro shape JSON da API.
func folderInfosFromListData(mailboxes []*imap.ListData) []folderInfo {
	folders := make([]folderInfo, 0, len(mailboxes))
	for _, mbox := range mailboxes {
		info := folderInfo{Name: mbox.Mailbox}
		// mbox.Status vem nil pra pastas \Noselect (ex: nós puramente
		// hierárquicos), que não têm mensagens.
		if mbox.Status != nil {
			info.Messages = derefUint32(mbox.Status.NumMessages)
			info.Unread = derefUint32(mbox.Status.NumUnseen)
		}
		folders = append(folders, info)
	}
	return folders
}

// writeIMAPError traduz um erro vindo de imapx.WithClient pra resposta
// HTTP — 401 se foi falha de autenticação, 502 pra qualquer outro erro
// de comunicação com o servidor IMAP (logado com msg de contexto).
func writeIMAPError(w http.ResponseWriter, err error, msg string) {
	if errors.Is(err, imapx.ErrAuthFailed) {
		writeError(w, http.StatusUnauthorized, "credenciais IMAP inválidas")
		return
	}
	log.Printf("%s: %v", msg, err)
	writeError(w, http.StatusBadGateway, "falha ao comunicar com o servidor IMAP")
}

// derefUint32 retorna 0 se p for nil — o servidor IMAP pode não
// devolver algum item de STATUS solicitado.
func derefUint32(p *uint32) uint32 {
	if p == nil {
		return 0
	}
	return *p
}
