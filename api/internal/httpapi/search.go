package httpapi

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"imap-api/internal/config"
	"imap-api/internal/imapx"
)

// searchDateLayout é o formato aceito pelos parâmetros de query
// since/before de GET /emails/search.
const searchDateLayout = "2006-01-02"

// searchAttachmentText é a heurística usada pra filtrar por hasAttachment
// — igual à API legada (webmail-api): o IMAP não tem uma chave de SEARCH
// nativa pra "tem anexo" (isso só existe no FETCH de BODYSTRUCTURE, não
// no SEARCH), então busca por "filename=" via TEXT (presente no
// Content-Disposition de uma parte com anexo). Best-effort: pode dar
// falso positivo (a string aparece em outro lugar da mensagem) ou falso
// negativo (cliente que gerou o e-mail não inclui "filename=" no
// Content-Disposition) — o campo hasAttachment devolvido em cada
// resultado continua sendo calculado de verdade (via BODYSTRUCTURE, ver
// emailInfoFromMessage), só o filtro de busca em si é que é aproximado.
const searchAttachmentText = "filename="

// searchEmailInfo é o item de resultado de GET /emails/search: mesmo
// shape de emailInfo (a listagem de GET /folders/{pasta}/emails), mais a
// pasta onde a mensagem foi encontrada — necessário aqui porque a busca
// pode abranger várias pastas ao mesmo tempo.
type searchEmailInfo struct {
	emailInfo
	Folder string `json:"folder"`
}

// searchResponse é o body de GET /emails/search.
type searchResponse struct {
	Emails     []searchEmailInfo `json:"emails"`
	Page       int               `json:"page"`
	PageSize   int               `json:"pageSize"`
	Total      int               `json:"total"`
	TotalPages int               `json:"totalPages"`
}

// searchFilters são os critérios de busca de GET /emails/search,
// convertidos pra um imap.SearchCriteria em toCriteria. hasAttachment/
// starred/unread usam *bool (em vez de bool) pra distinguir "não
// informado" (não filtra) de "false" (filtra pelo oposto).
type searchFilters struct {
	query         string
	from          string
	to            string
	subject       string
	hasAttachment *bool
	starred       *bool
	unread        *bool
	since         time.Time
	before        time.Time
}

// toCriteria converte os filtros pro critério de busca do go-imap.
// Múltiplos campos preenchidos são combinados com E (interseção) — ver
// doc de imap.SearchCriteria.
func (f searchFilters) toCriteria() *imap.SearchCriteria {
	criteria := &imap.SearchCriteria{}

	if f.query != "" {
		criteria.Text = append(criteria.Text, f.query)
	}
	if f.from != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: f.from})
	}
	if f.to != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "To", Value: f.to})
	}
	if f.subject != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: f.subject})
	}

	if f.hasAttachment != nil {
		if *f.hasAttachment {
			criteria.Text = append(criteria.Text, searchAttachmentText)
		} else {
			criteria.Not = append(criteria.Not, imap.SearchCriteria{Text: []string{searchAttachmentText}})
		}
	}

	if f.unread != nil {
		if *f.unread {
			criteria.NotFlag = append(criteria.NotFlag, imap.FlagSeen)
		} else {
			criteria.Flag = append(criteria.Flag, imap.FlagSeen)
		}
	}

	if f.starred != nil {
		if *f.starred {
			criteria.Flag = append(criteria.Flag, imap.FlagFlagged)
		} else {
			criteria.NotFlag = append(criteria.NotFlag, imap.FlagFlagged)
		}
	}

	if !f.since.IsZero() {
		criteria.Since = f.since
	}
	if !f.before.IsZero() {
		criteria.Before = f.before
	}

	return criteria
}

// searchEmailsParams são os parâmetros de query de GET /emails/search.
// folder vazio significa "todas as pastas".
type searchEmailsParams struct {
	paginationParams
	folder  string
	filters searchFilters
}

// parseSearchEmailsParams lê e valida os parâmetros de GET
// /emails/search: page/pageSize/oldestFirst (ver parsePaginationParams),
// folder (opcional), e os filtros de busca (todos opcionais, mas ao
// menos um costuma fazer sentido — nenhum filtro equivale a SEARCH ALL).
func parseSearchEmailsParams(r *http.Request) (searchEmailsParams, error) {
	query := r.URL.Query()

	pagination, err := parsePaginationParams(query)
	if err != nil {
		return searchEmailsParams{}, err
	}

	params := searchEmailsParams{
		paginationParams: pagination,
		folder:           query.Get("folder"),
		filters: searchFilters{
			query:   query.Get("query"),
			from:    query.Get("from"),
			to:      query.Get("to"),
			subject: query.Get("subject"),
		},
	}

	if v := query.Get("hasAttachment"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return searchEmailsParams{}, fmt.Errorf("hasAttachment inválido (%q): precisa ser true ou false", v)
		}
		params.filters.hasAttachment = &b
	}

	if v := query.Get("starred"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return searchEmailsParams{}, fmt.Errorf("starred inválido (%q): precisa ser true ou false", v)
		}
		params.filters.starred = &b
	}

	if v := query.Get("unread"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return searchEmailsParams{}, fmt.Errorf("unread inválido (%q): precisa ser true ou false", v)
		}
		params.filters.unread = &b
	}

	if v := query.Get("since"); v != "" {
		t, err := time.Parse(searchDateLayout, v)
		if err != nil {
			return searchEmailsParams{}, fmt.Errorf("since inválido (%q): precisa estar no formato AAAA-MM-DD", v)
		}
		params.filters.since = t
	}

	if v := query.Get("before"); v != "" {
		t, err := time.Parse(searchDateLayout, v)
		if err != nil {
			return searchEmailsParams{}, fmt.Errorf("before inválido (%q): precisa estar no formato AAAA-MM-DD", v)
		}
		params.filters.before = t
	}

	return params, nil
}

// searchMatch é uma mensagem encontrada pela busca, com a pasta onde foi
// encontrada — necessário porque handleSearchEmails ordena/pagina o
// resultado combinado de (potencialmente) várias pastas antes de montar
// o JSON de resposta.
type searchMatch struct {
	folder string
	msg    *imapclient.FetchMessageBuffer
}

// handleSearchEmails implementa GET /emails/search: busca mensagens por
// texto/remetente/destinatário/assunto/anexo/favorito/lida ou não/data,
// numa pasta específica (query "folder") ou em todas as pastas da conta
// (folder ausente). Sem "folder", cada pasta é buscada em paralelo (uma
// goroutine + conexão IMAP própria por pasta, já que uma única conexão
// só pode ter uma pasta selecionada por vez) — o grau de paralelismo
// vem de search.folderConcurrency (config), igual ao MAX_THREADS_ALL_FOLDERS
// da API legada (webmail-api). O resultado combinado é ordenado por data
// (mesma regra de GET /folders/{pasta}/emails) e paginado em memória.
//
// @Summary Busca e-mails
// @Description Busca por texto livre e/ou filtros estruturados (remetente, destinatário, assunto, anexo, favorito, lida/não lida, data), numa pasta específica ou em todas as pastas da conta. Sem "folder", busca em paralelo em todas as pastas (grau de paralelismo configurável via search.folderConcurrency) e mergeia o resultado. hasAttachment usa uma heurística de texto (não há chave de SEARCH nativa pra isso no IMAP) — pode ter falso positivo/negativo; o campo hasAttachment de cada resultado, esse sim, é calculado de verdade via BODYSTRUCTURE.
// @Tags e-mails
// @Produce json
// @Param folder query string false "Restringe a busca a essa pasta (barra de nível aninhado como %2F); se omitido, busca em todas as pastas"
// @Param query query string false "Texto livre (assunto, remetentes, destinatários e corpo)"
// @Param from query string false "Filtra pelo remetente (header From)"
// @Param to query string false "Filtra pelo destinatário (header To)"
// @Param subject query string false "Filtra pelo assunto"
// @Param hasAttachment query bool false "Filtra por ter (true) ou não ter (false) anexo — heurística de texto, não exata"
// @Param starred query bool false "Filtra por favoritada (true) ou não (false)"
// @Param unread query bool false "Filtra por não lida (true) ou lida (false)"
// @Param since query string false "Mensagens a partir dessa data, inclusive (AAAA-MM-DD)"
// @Param before query string false "Mensagens antes dessa data (AAAA-MM-DD)"
// @Param page query int false "Página (padrão 1)"
// @Param pageSize query int false "Itens por página, 10-100 (padrão 50)"
// @Param oldestFirst query bool false "Inverte a ordenação padrão (mais recente primeiro) pra mais antiga primeiro"
// @Param imapUser header string true "Usuário IMAP"
// @Param imapPassword header string true "Senha IMAP"
// @Success 200 {object} searchResponse
// @Failure 400 {object} errorResponse "parâmetros inválidos (ver descrição de cada um)"
// @Failure 401 {object} errorResponse "headers imapUser/imapPassword ausentes, ou credenciais inválidas"
// @Failure 404 {object} errorResponse "pasta não encontrada (só quando folder é informado)"
// @Failure 502 {object} errorResponse "falha ao comunicar com o servidor IMAP"
// @Router /emails/search [get]
func handleSearchEmails(imapCfg config.IMAPConfig, searchCfg config.SearchConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params, err := parseSearchEmailsParams(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		creds, ok := credentialsFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "headers imapUser e imapPassword são obrigatórios")
			return
		}

		criteria := params.filters.toCriteria()

		var matches []searchMatch
		if params.folder != "" {
			err = imapx.WithClient(imapCfg, creds.user, creds.password, func(c *imapclient.Client) error {
				m, err := searchFolder(c, params.folder, criteria)
				matches = m
				return err
			})
			if err != nil {
				if errors.Is(err, errFolderNotFound) {
					writeError(w, http.StatusNotFound, "pasta não encontrada")
					return
				}
				writeIMAPError(w, err, "erro ao buscar e-mails")
				return
			}
		} else {
			var folders []string
			err = imapx.WithClient(imapCfg, creds.user, creds.password, func(c *imapclient.Client) error {
				mailboxes, err := c.List("", "*", nil).Collect()
				if err != nil {
					return err
				}
				folders = searchableFolderNames(mailboxes)
				return nil
			})
			if err != nil {
				writeIMAPError(w, err, "erro ao listar pastas pra busca")
				return
			}

			matches = searchAllFolders(imapCfg, creds, folders, criteria, searchCfg.FolderConcurrency)
		}

		sort.Slice(matches, func(i, j int) bool {
			if params.oldestFirst {
				return envelopeDate(matches[i].msg).Before(envelopeDate(matches[j].msg))
			}
			return envelopeDate(matches[i].msg).After(envelopeDate(matches[j].msg))
		})

		writeJSON(w, http.StatusOK, paginateSearch(matches, params.paginationParams))
	}
}

// searchFolder seleciona folder em modo leitura (EXAMINE, ReadOnly) —
// igual GET /folders/{pasta}/emails, este endpoint só lê e nunca deve
// marcar mensagens como \Seen — faz o SEARCH com criteria e, se achar
// alguma mensagem, busca ENVELOPE/FLAGS/BODYSTRUCTURE de todas (mesmos
// campos de GET /folders/{pasta}/emails, necessários pra montar cada
// searchEmailInfo).
func searchFolder(c *imapclient.Client, folder string, criteria *imap.SearchCriteria) ([]searchMatch, error) {
	if _, err := c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		if isNonExistentMailbox(err) {
			return nil, errFolderNotFound
		}
		return nil, err
	}

	searchData, err := c.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}

	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}

	messages, err := c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		UID:      true,
		Envelope: true,
		Flags:    true,
		// Extended: true é o que traz Content-Disposition (usado em
		// hasAttachment) — ver mesma nota em handleListEmails.
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
	}).Collect()
	if err != nil {
		return nil, err
	}

	matches := make([]searchMatch, 0, len(messages))
	for _, msg := range messages {
		matches = append(matches, searchMatch{folder: folder, msg: msg})
	}
	return matches, nil
}

// searchableFolderNames devolve os nomes das pastas de mailboxes que
// podem ser selecionadas (SELECT/EXAMINE) — pastas \Noselect (nós
// puramente hierárquicos, ex: "Trabalho" quando só "Trabalho/Projetos"
// tem mensagens) não entram, já que um SELECT nelas falha.
func searchableFolderNames(mailboxes []*imap.ListData) []string {
	names := make([]string, 0, len(mailboxes))
	for _, mbox := range mailboxes {
		if slices.Contains(mbox.Attrs, imap.MailboxAttrNoSelect) {
			continue
		}
		names = append(names, mbox.Mailbox)
	}
	return names
}

// searchAllFolders busca em todas as folders em paralelo, uma goroutine
// e uma conexão IMAP própria por pasta (imapx.WithClient abre uma
// conexão nova a cada chamada — uma única conexão só pode ter uma pasta
// selecionada por vez, então não dá pra compartilhar conexão entre
// goroutines aqui), limitado a concurrency pastas simultâneas. Erro numa
// pasta específica (ex: um SELECT que falha por algum motivo transiente)
// não derruba a busca inteira — só é logado e a pasta é pulada, mesmo
// comportamento da API legada (search_service.rb#all_folders).
func searchAllFolders(imapCfg config.IMAPConfig, creds credentials, folders []string, criteria *imap.SearchCriteria, concurrency int) []searchMatch {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []searchMatch

	for _, folder := range folders {
		wg.Add(1)
		sem <- struct{}{}
		go func(folder string) {
			defer wg.Done()
			defer func() { <-sem }()

			var matches []searchMatch
			err := imapx.WithClient(imapCfg, creds.user, creds.password, func(c *imapclient.Client) error {
				m, err := searchFolder(c, folder, criteria)
				matches = m
				return err
			})
			if err != nil {
				log.Printf("busca: erro ao buscar na pasta %q: %v", folder, err)
				return
			}

			mu.Lock()
			results = append(results, matches...)
			mu.Unlock()
		}(folder)
	}

	wg.Wait()
	return results
}

// paginateSearch recorta matches (já ordenada) pra página pedida e monta
// o body de resposta com os metadados de paginação — mesma lógica de
// paginate (GET /folders/{pasta}/emails), mas acrescentando a pasta de
// cada resultado.
func paginateSearch(matches []searchMatch, params paginationParams) searchResponse {
	total := len(matches)
	start, end, totalPages := paginationWindow(total, params)

	page := matches[start:end]
	emails := make([]searchEmailInfo, 0, len(page))
	for _, m := range page {
		emails = append(emails, searchEmailInfo{
			emailInfo: emailInfoFromMessage(m.msg),
			Folder:    m.folder,
		})
	}

	return searchResponse{
		Emails:     emails,
		Page:       params.page,
		PageSize:   params.pageSize,
		Total:      total,
		TotalPages: totalPages,
	}
}
