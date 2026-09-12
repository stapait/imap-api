# API

Projeto da API HTTP em si, em Go. Código em `cmd/` (entrypoint) e `internal/` (não importável de fora do módulo).

## Stack

- **Linguagem:** Go, escolhida por performance e concorrência.
- **Concorrência:** operações que podem rodar em paralelo (ex: buscar múltiplos e-mails, checar status de várias pastas) devem usar goroutines.
- **Router HTTP:** `net/http` puro da stdlib (Go 1.22+, com path patterns tipo `"GET /folders"` no `ServeMux`) — sem framework/router externo.
- **Cliente IMAP:** [`github.com/emersion/go-imap/v2`](https://github.com/emersion/go-imap) (atualmente `v2.0.0-beta.8`) + [`github.com/emersion/go-message`](https://github.com/emersion/go-message) para parsing de MIME — combo mais ativo/moderno do ecossistema Go pra isso. **Atenção:** o v2 do go-imap ainda está em beta (pré-1.0), a API pode ter breaking changes até a versão estável — pin exato no `go.mod`.
- **Configuração:** arquivo YAML (`config/config.<ambiente>.yaml`, parseado com `gopkg.in/yaml.v3`, sem framework de config), com o endereço do servidor IMAP como uma das configurações — ver `internal/config`.
- **Layout do projeto:** `cmd/server` (entrypoint) + `internal/...` (handlers, cliente IMAP, config).
- **Task runner:** [`Taskfile.yml`](https://taskfile.dev) (`go install github.com/go-task/task/v3/cmd/task@latest`) — comandos nomeados tipo `task test:integration`, análogo aos scripts do `package.json`.
- **Documentação da API:** Swagger 2.0/OpenAPI, gerado automaticamente a partir de comentários de anotação (`@Summary`, `@Param`, `@Success` etc.) acima de cada handler, via [`swaggo/swag`](https://github.com/swaggo/swag) — não se escreve o spec à mão. Servido no próprio servidor em `/swagger` (UI) via [`swaggo/http-swagger`](https://github.com/swaggo/http-swagger). A descrição completa de cada endpoint (parâmetros, shapes de request/response) mora nessas anotações + no spec gerado (`docs/`) — este README fica com o design de alto nível e as decisões/pendências em aberto durante o desenvolvimento.

## Modelo de conta

Multi-conta: cada requisição carrega as credenciais IMAP da conta a ser acessada, nos headers `imapUser`/`imapPassword` — a API não guarda usuário nenhum, só repassa pro servidor IMAP configurado. O servidor IMAP em si (host/porta/TLS) é fixo na configuração (`internal/config`), não vem por requisição — em desenvolvimento, aponta pro Dovecot local do `docker/`.

**Uso interno:** esta API nunca é chamada direto pelo browser — quem fala com ela é sempre um backend (ex: o backend de um webmail, que recebe a requisição do browser dele e repassa pra cá). Isso libera a API de restrições pensadas pra chamada direta de browser (CORS, caracteres "seguros" de URL, etc.) — ver por exemplo a convenção de `%2F` pra pasta com sub-pastas em `GET /folders/{pasta}` abaixo.

## Endpoints (rascunho)

Primeira rodada de brainstorm, cobrindo os casos de uso já identificados. Envio de e-mail (SMTP) está fora do escopo desta API — decisão definitiva, não uma pendência.

### Operacional

- ✅ `GET /health` — **implementado.** Health check de vida do processo: confirma só que a API está no ar e respondendo. Não verifica conectividade com o servidor IMAP configurado nem nenhuma outra dependência externa — e por isso, diferente de todo outro endpoint, não exige os headers `imapUser`/`imapPassword` (não fala com nenhuma conta específica). `200 {"status": "ok"}`.

### Conta

- ✅ `GET /account` — **implementado.** Quota de armazenamento (`STORAGE`) da conta autenticada, em bytes:
  ```json
  {"quotaTotal": 1073741824, "quotaUsed": 52428800}
  ```
  O IMAP (`GETQUOTAROOT`, RFC 9208) devolve os valores em KiB (múltiplos de 1024 bytes) — convertidos aqui pra bytes. `quotaTotal`/`quotaUsed` vêm `null` (não `0`) se a conta não tiver quota de `STORAGE` configurada, ou se o servidor não suportar a extensão `QUOTA` — degrada graciosamente, não é erro (é exatamente o que acontece contra o Dovecot local, que não tem o plugin de quota habilitado). Sem `mailbox_status` (campo que a API legada tem, mas que é só um vestígio de suportar vários provedores de e-mail — pro Dovecot, ela mesma sempre devolve "ok" direto após o login; nesta API, falha de autenticação já vira 401 antes de qualquer resposta ser montada, então esse campo nunca variaria).

### Pastas

- ✅ `GET /folders` — **implementado.** Lista as pastas IMAP disponíveis na conta, com nome, total de mensagens e não lidas de cada uma:
  ```json
  {"folders": [{"name": "INBOX", "messages": 123, "unread": 2}]}
  ```
  `unread` é a contagem de mensagens sem a flag `\Seen` (STATUS `UNSEEN`, obtido via LIST-STATUS — RFC 5819 — numa única ida-e-volta pra todas as pastas). Nota: o IMAP não tem um conceito de "não lida" separado de `\Seen` — não existe uma flag nativa distinta que também conte como "não lida" sem ser "não vista".
  Ainda de fora: delimitador hierárquico, UIDVALIDITY/UIDNEXT.
- ✅ `GET /folders/{pasta}` — **implementado.** Mesmo body de `GET /folders`, mas restrito à pasta pedida e suas sub-pastas (diretas ou não) — não inclui as demais pastas da conta. 404 se `{pasta}` não existir. Como o delimitador hierárquico do Dovecot local é `/` (igual ao separador de path da URL), uma pasta aninhada é passada com a barra **percent-encoded (`%2F`)**, ex: `GET /folders/Trabalho%2FProjetos` pra acessar a sub-pasta `Trabalho/Projetos` diretamente — a barra literal, não codificada, não é aceita (vira mais um segmento de path e dá 404, já que o `{pasta}` da rota é de um segmento só). Essa convenção existe pra já ficar compatível com as rotas aninhadas futuras tipo `GET /folders/{pasta}/emails`, onde uma barra literal na pasta colidiria de verdade com o separador de path da própria rota (`{pasta}` não pode ser um wildcard de múltiplos segmentos no meio do pattern) — ver nota de "uso interno" acima.
  Em aberto: UIDVALIDITY/UIDNEXT de uma pasta específica (mencionados acima como "ainda de fora" de `GET /folders`) ainda não têm endpoint — decidir se entram aqui ou em rota própria quando fizer sentido separar.
- ✅ `DELETE /folders/{pasta}` — **implementado.** Apaga a pasta pedida e todas as suas sub-pastas (diretas ou não), recursivamente — apaga da mais aninhada até a pedida por último, já que servidores IMAP podem recusar apagar uma pasta que ainda tem filhas. `204 No Content` no sucesso. 404 se `{pasta}` não existir. `400` se `{pasta}` for `INBOX` (case-insensitive) — a INBOX nunca pode ser apagada, regra do próprio protocolo IMAP (RFC 3501), validada pela API antes de tentar no servidor.
- ✅ `POST /folders/{pasta}` — **implementado.** Cria a pasta pedida (barra de nível aninhado como `%2F` pra criar direto uma sub-pasta). Sem validação própria de nome (tamanho, caracteres) — só repassa pro `CREATE` do IMAP e traduz o erro real do servidor, igual o resto da API já faz pra outras validações que o próprio protocolo garante. `201 Created` no sucesso, body `{"name": "...", "messages": 0, "unread": 0}` (mesmo shape de `GET /folders`, sem precisar de uma ida extra ao servidor já que uma pasta recém-criada é sempre vazia). `409 Conflict` se a pasta já existir.
- ✅ `PATCH /folders/{pasta}` — **implementado.** Renomeia a pasta pedida — body `{"name": "NovoNome"}`. Mesma filosofia de validação do `POST` (confia no servidor). `200` no sucesso, mesmo shape de `GET /folders/{pasta}` (já refletindo o novo nome). `400` se `name` estiver ausente/vazio, ou se `{pasta}` for a INBOX — RFC 3501 define uma semântica especial pra "RENAME INBOX" (não renomeia a INBOX em si, só cria a pasta nova e move as mensagens pra lá, deixando a INBOX vazia) surpreendente demais pra expor, por isso bloqueada. `404` se `{pasta}` não existir. `409` se já existir uma pasta com o novo nome.

### E-mails

- ✅ `GET /folders/{pasta}/emails?page=1&pageSize=50&oldestFirst=false` — **implementado.** Listagem leve para exibição em lista (ex: inbox de um frontend): não faz parsing do e-mail inteiro, só os campos necessários pra uma UI de listagem. Equivale a um IMAP `FETCH` de `UID` + `ENVELOPE` + `BODYSTRUCTURE` (com `Extended: true`, pra trazer `Content-Disposition`) + `FLAGS`, sem baixar o corpo. 404 se `{pasta}` não existir (mesma regra do `GET /folders/{pasta}`, `%2F` incluso).
  ```json
  {
    "emails": [{"uid": 123, "subject": "Assunto", "from": "Fulano de Tal", "hasAttachment": false, "forwarded": false}],
    "page": 1, "pageSize": 50, "total": 557, "totalPages": 12
  }
  ```
  - `page` (padrão `1`) e `pageSize` (padrão `50`, mín. `10`, máx. `100`) — paginação em memória sobre a pasta inteira (o FETCH de `ENVELOPE`/`BODYSTRUCTURE` de todas as mensagens já precisa acontecer pra poder ordenar por data; não tem como pedir só uma página direto do IMAP sem SORT). `page` além do fim não é erro, só devolve `emails: []`. `pageSize` fora de `[10, 100]`, ou `page`/`pageSize` não-numéricos, dão 400.
  - `oldestFirst` (opcional, padrão `false`) — inverte a ordenação padrão (mais recente primeiro, por `Date` do ENVELOPE) pra mais antiga primeiro. Valor que não seja `true`/`false` dá 400.
  - `total`/`totalPages`: total de mensagens da pasta e de páginas com o `pageSize` pedido — não mudam entre páginas da mesma pasta.
  - `uid`: UID da mensagem.
  - `from`: nome de exibição do primeiro remetente, com fallback pro e-mail quando não há nome (não inclui os dois juntos, nem os demais remetentes de `Cc`/`To`).
  - `hasAttachment`: `true` se alguma parte do BODYSTRUCTURE tiver `Content-Disposition: attachment` — parte inline (ex: imagem embutida no corpo HTML) não conta.
  - `forwarded`: `true` se a mensagem tiver a flag/keyword IMAP `$Forwarded` — depende do cliente de e-mail que originou o forward tê-la setado (não é detectado por heurística de assunto tipo "Fwd:").
  Ainda de fora: filtro de flags (lida/não lida), tamanho (`RFC822.SIZE`) — do rascunho original, não incluídos nesta versão.
- ✅ `DELETE /folders/{pasta}/emails` — **implementado.** Dois comportamentos, conforme o body:
  - **Sem body** (ou `uids` vazio/ausente): esvazia a pasta inteira — marca `\Deleted` em todas as mensagens e faz `EXPUNGE`, sempre definitivo (não passa pela lixeira) — a pasta em si continua existindo, só fica vazia. Comportamento inalterado desde a versão original deste endpoint.
  - **Com `{"uids": [123, 456]}` no body**: apaga só essas mensagens específicas, "lixeira-aware" — move pra uma pasta chamada **`Trash`** (nome fixo hardcoded, não descoberto via SPECIAL-USE nem informado por quem chama — decisão registrada em `CLAUDE.md`; não cobre contas cuja lixeira real se chame diferente, ex: "Lixeira", "Deleted Items"), a menos que `{pasta}` já seja a própria `Trash`, caso em que apaga definitivo (`UID EXPUNGE`, escopado só aos `uids` informados — não um `EXPUNGE` comum, que arrastaria qualquer outra mensagem que porventura já estivesse `\Deleted` na pasta por outro motivo).
  
  `204 No Content` no sucesso (nos dois casos), inclusive se a pasta já estiver vazia ou os `uids` não existirem mais (idempotente). `404` se `{pasta}` não existir, ou (só no caso com `uids`, movendo pra lixeira) se a pasta `Trash` não existir na conta.
- ✅ `PATCH /folders/{pasta}/emails/flags` — **implementado.** Adiciona/remove, em lote, flags das mensagens de `{pasta}` — body `{"uids": [123, 456], "read": true, "starred": false, "answered": true, "forwarded": true}`, onde `uids` é obrigatório (não pode ser vazio) e cada flag é opcional (só a que vier como `true`/`false` é alterada; ao menos uma precisa vir). Pensado pra crescer sem quebrar nada — uma flag nova vira só mais um campo opcional. Mapeamento pra flag IMAP (mesmos nomes que `GET /folders/{pasta}/emails/{uid}` já usa pros campos de leitura equivalentes): `read` → `\Seen`, `starred` → `\Flagged`, `answered` → `\Answered`, `forwarded` → `$Forwarded` (keyword, não flag de sistema). `204 No Content` no sucesso. `400` se `uids` estiver vazio, ou nenhuma flag for informada. `404` se `{pasta}` não existir.
- ✅ `PATCH /folders/{pasta}/emails/move` — **implementado.** Move, em lote, as mensagens de `{pasta}` pra outra pasta — body `{"uids": [123, 456], "destinationFolder": "PastaDestino"}`, os dois campos obrigatórios. `204 No Content` no sucesso. `400` se `uids` estiver vazio, ou `destinationFolder` ausente. `404` se `{pasta}` (origem) não existir, ou se `destinationFolder` apontar pra uma pasta que não existe.
- ✅ `GET /folders/{pasta}/emails/{uid}` — **implementado.** E-mail completo: metadados estruturados (equivalente ao ENVELOPE/FLAGS do IMAP — nunca um bloco de headers técnicos crus, tipo `Message-ID`/`Received`/`Return-Path`) mais o corpo parseado (texto ou HTML, o que a mensagem tiver — HTML tem preferência quando os dois existem, via `multipart/alternative`) e a lista leve dos anexos (nome/tipo/tamanho, sem o conteúdo binário). 404 se `{pasta}` ou `{uid}` (UID da mensagem) não existirem.
  ```json
  {
    "uid": 123,
    "subject": "Assunto",
    "from": {"name": "Fulano de Tal", "email": "fulano@example.com"},
    "to": [{"name": "", "email": "destino@example.com"}],
    "cc": [],
    "date": "2026-03-10T08:00:00Z",
    "read": true, "starred": false, "answered": false, "forwarded": false,
    "mimeType": "text/html",
    "body": "<p>Corpo em HTML.</p>",
    "attachments": [{"filename": "anexo.txt", "contentType": "text/plain", "size": 17, "attachmentLink": "/folders/INBOX/emails/123/attachments/MDphbmV4by50eHQ"}]
  }
  ```
  - Diferente dos demais endpoints (que usam `EXAMINE`/`Peek` e nunca alteram o servidor), este faz um `Select` em modo leitura-escrita — buscar o corpo aqui **marca a mensagem como lida** (`\Seen`), como um cliente de e-mail faz ao abrir uma mensagem. `read` na resposta já reflete isso (sempre `true`, exceto num erro).
  - `from`/`to`/`cc`: mesmo formato de endereço (`{name, email}`); `from` é só o primeiro remetente (não há campo `bcc` — o IMAP não devolve `Bcc` de mensagens recebidas).
  - `attachments`: mesma regra de `hasAttachment` da listagem — só partes com `Content-Disposition: attachment` contam; parte inline (ex: imagem embutida no corpo HTML) não aparece aqui. `attachmentLink` é o path pronto de `GET /folders/{pasta}/emails/{uid}/attachments/{attachmentId}` (ver seção "Anexos" abaixo) — não é pra ser montado à mão, só usado como veio.
  - `mimeType`/`body`: `""`/`""` se a mensagem não tiver nenhuma parte de texto (raro, mas possível — ex: só anexo, sem corpo).
- ✅ `GET /folders/{pasta}/emails/{uid}/raw` — **implementado.** Fonte crua da mensagem original (RFC 822), sem nenhum parsing — útil pra debug ou export. `Content-Type: message/rfc822`, `Content-Disposition: attachment; filename="{uid}.eml"`. Select em modo leitura (`EXAMINE`) — como o download de anexo, e diferente de `GET /folders/{pasta}/emails/{uid}`, baixar o raw **não** marca a mensagem como lida (é operação de export, não "abrir a mensagem"). 404 se `{pasta}` ou `{uid}` não existirem.
- ✅ `GET /emails/search?folder=INBOX&query=...&from=...&to=...&subject=...&hasAttachment=true&starred=true&unread=true&since=2026-01-01&before=2026-03-01&page=1&pageSize=50&oldestFirst=false` — **implementado.** Busca mensagens por texto livre e/ou filtros estruturados, mapeando pro IMAP `SEARCH`. `folder` é opcional — se omitido, busca em **todas** as pastas da conta em paralelo (uma goroutine + conexão IMAP própria por pasta, já que uma única conexão só tem uma pasta selecionada por vez; grau de paralelismo configurável em `search.folderConcurrency`, ver `config/config.example.yaml`) e mergeia o resultado. Mesmo shape de item de `GET /folders/{pasta}/emails` (`emailInfo`), acrescido do campo `folder` (pasta onde a mensagem foi encontrada) — necessário porque a busca pode abranger várias pastas ao mesmo tempo. Paginação/ordenação iguais a `GET /folders/{pasta}/emails` (`page`/`pageSize`/`oldestFirst`, por data). Filtros, todos opcionais e combináveis (E lógico entre os informados): `query` (texto livre em assunto/remetentes/destinatários/corpo, IMAP `TEXT`), `from`/`to`/`subject` (busca em header), `hasAttachment`/`starred`/`unread` (booleanos), `since`/`before` (data `AAAA-MM-DD`, filtram pelo INTERNALDATE da mensagem — data de entrega na pasta, não o header `Date:`). Nenhum filtro informado equivale a `SEARCH ALL` (devolve tudo). `hasAttachment` usa uma heurística de texto (`TEXT "filename="`) — o IMAP não tem uma chave de `SEARCH` nativa pra "tem anexo" (só existe no `FETCH` de `BODYSTRUCTURE`, não no `SEARCH`) — mesma abordagem da API legada (`webmail-api`); pode dar falso positivo/negativo, mas o campo `hasAttachment` de cada resultado é sempre calculado de verdade (via `BODYSTRUCTURE`). 404 se `folder` for informado e não existir (busca em todas as pastas nunca dá 404 — pastas com erro individual, ex: falha transiente de `SELECT`, são só puladas e logadas, não derrubam a busca inteira).

### Anexos

- ✅ `GET /folders/{pasta}/emails/{uid}/attachments/{attachmentId}` — **implementado.** Descartada a ideia de uma listagem separada (`GET /folders/{pasta}/emails/{uid}/attachments`) — `GET /folders/{pasta}/emails/{uid}` já devolve essa mesma lista dentro do campo `attachments`, com `attachmentLink` pronto pra cada anexo, sem necessidade de um endpoint dedicado só pra isso. Conteúdo binário de um anexo específico (`Content-Type` do anexo, `Content-Disposition: attachment` com o nome do arquivo). `attachmentId` é um id sintético opaco — base64 (URL-safe, sem padding) de `"índice:nome do arquivo"`, onde índice é a posição do anexo dentro da mensagem (na mesma ordem em que aparece no array `attachments` de `GET /folders/{pasta}/emails/{uid}`) — não é pra ser montado à mão pelo cliente da API, sempre vem pronto no campo `attachmentLink` de cada item de `attachments`. O nome do arquivo dentro do id é só cosmético (dá pra reconhecer do que se trata só de olhar o id) — o índice é a única parte usada de verdade pra localizar o anexo; existe porque nome de anexo não é garantido único dentro de uma mensagem (nem sempre presente, em mensagens malformadas). Select em modo leitura (`EXAMINE`) — diferente de `GET /folders/{pasta}/emails/{uid}`, baixar um anexo **não** marca a mensagem como lida. 404 se `{pasta}`, `{uid}` ou `{attachmentId}` não existirem/forem inválidos (id mal formado, ou índice fora do alcance da mensagem).

## Pendências para decidir depois

Nenhuma pendência do rascunho original em aberto — todas as operações de escrita cogitadas (mover, apagar, marcar lida/não lida, favoritar/desfavoritar mensagem, criar/renomear/apagar/esvaziar pasta) estão implementadas (ver acima). Itens que surgiram só da comparação com a API legada (`unseen_mail`, `recent_mail` — nunca fizeram parte deste rascunho) estão registrados no `CLAUDE.md`, não aqui.

Fora do escopo desta API, por decisão (não pendências): envio de e-mail (SMTP) e qualquer funcionalidade que exija conexão com banco de dados próprio — a API não guarda estado nenhum além do que chega por requisição; se algum dia precisar de um dado hoje só disponível em banco (ex: nome de exibição preferido), ele chega como parâmetro da requisição, não por uma consulta a um banco que a API mantém.

## Como rodar localmente

Com o Dovecot local rodando (`docker compose up -d` em `../docker/`):

```bash
cd api
go run ./cmd/server
```

Por padrão lê `config/config.dev.yaml` (aponta pro Dovecot local); para usar outro arquivo, defina `CONFIG_PATH`.

```bash
curl http://localhost:8080/folders -H "imapUser: testuser" -H "imapPassword: 123"
```

Com o servidor de pé, a documentação interativa (Swagger UI) fica em [`http://localhost:8080/swagger/`](http://localhost:8080/swagger/) — o spec bruto (JSON) fica em `/swagger/doc.json` (`/swagger/doc.yaml` pro YAML).

### Regenerando a documentação (Swagger)

O spec (`docs/docs.go`, `docs/swagger.json`, `docs/swagger.yaml`) é gerado a partir das anotações `@...` acima de cada handler (ver `internal/httpapi/*.go`) pelo [`swag`](https://github.com/swaggo/swag) — instalado como tool dependency do módulo (`go.mod`, bloco `tool`), sem precisar de instalação global. `docs/` é versionado (não fica no `.gitignore`), então depois de adicionar/mudar um endpoint ou suas anotações, regenere e inclua o resultado no commit:

```bash
task swagger:generate
```

Ou, sem o `task`:

```bash
go generate ./...
```

## Testes

O runner é o [`task`](https://taskfile.dev) (ver `Taskfile.yml`). Se ainda não tiver:

```bash
go install github.com/go-task/task/v3/cmd/task@latest   # e garanta $(go env GOPATH)/bin no PATH
```

- `task test:unit` — testes unitários (rápidos, sem dependências externas). Ainda não há nenhum.
- `task test:integration` — um teste de integração por endpoint, batendo no servidor HTTP real por cima do Dovecot local (`api/test/integration/`). Pode demorar — depende de rede/IO real, não é mockado.

Os testes de integração ficam atrás da build tag `integration` (`go:build integration`) justamente pra não rodarem sem querer num `go test ./...` comum.

### Como rodar os testes de integração

**Pré-requisitos:**

1. **Dovecot local de pé** — `cd ../docker && docker compose up -d`. Se ele não estiver acessível no host:porta configurado, o `TestMain` falha na hora com a instrução do `docker compose up -d`, sem rodar teste nenhum.
2. **Fixtures `.eml` em `docker/emails/`** — essa pasta é gitignored e curada à mão: gere o material bruto com os scripts de `docker/generate-emails/` e organize os `.eml` em subpastas, uma por pasta IMAP (`docker/emails/INBOX/*.eml` → INBOX, etc.). Ver `docker/generate-emails/README.md` e `docker/populate-mailbox/README.md`. Se não houver nenhum `.eml`, o `TestMain` falha com essa mesma indicação.
3. **`python3` no PATH** — o `TestMain` executa `docker/populate-mailbox/populate_mailbox.py` (só stdlib, sem dependências a instalar).

**Rodando:**

```bash
cd api
task test:integration
```

Ou, sem o `task`:

```bash
go test -tags=integration -count=1 -v ./test/integration/...
```

O `-count=1` não é opcional: sem ele o cache de testes do Go pode mascarar uma falha real com um `(cached)` velho, já que o que mudou (o estado do Dovecot) está fora do código Go.

**O que acontece a cada rodada:** antes de qualquer teste, o `TestMain` (`test/integration/main_test.go`) carrega a config (`config/config.dev.yaml`, ou `CONFIG_PATH` se definido), checa os pré-requisitos acima e roda o `populate_mailbox.py`, que **apaga tudo** da caixa de teste (todas as pastas exceto INBOX, e esvazia a INBOX) antes de repopular a partir de `docker/emails/`. Ou seja: **os testes são destrutivos** para o conteúdo atual do Dovecot local — não guarde nada que importe lá. Depois disso ele sobe um `httptest.NewServer` com os handlers reais, compartilhado por todos os testes do pacote.

**Credenciais:** os testes logam com o usuário/senha definidos em `main_test.go` (`testIMAPUser`/`testIMAPPassword`). Essa senha precisa bater com `DOVECOT_MAILBOX_PASSWORD` (`docker/.env`) e com `MAILBOX_PASSWORD` em `populate_mailbox.py` — o auth padrão do Dovecot usa uma senha única compartilhada por qualquer username.

**Adicionando testes:** um arquivo por endpoint (ex: `folders_test.go`), sempre começando com `//go:build integration`.

**Teste de fumaça (obrigatório pra endpoints por mensagem):** todo endpoint que opere sobre uma mensagem específica (`{pasta}/{uid}` ou equivalente) precisa de um teste que itere toda mensagem real da caixa de teste e garanta que o endpoint nunca erra pra nenhuma delas — ver `smoke_test.go` (`allFolderEmailRefs`/`runSmokeTest`) e `TestGetEmail_SmokeAllMessages` em `email_test.go` pro padrão. É assim que a diversidade/malformação real dos fixtures (`docker/emails/`, `docker/emails-permanent/`, corpus real incluso) de fato é exercitada contra bugs de parsing — casos sintéticos isolados não substituem isso. Por causa disso, código de endpoint que processa conteúdo de mensagem (corpo, headers, estrutura MIME) deve degradar graciosamente diante de entrada malformada — devolver resultado parcial/vazio — em vez de falhar a requisição; ver `parseEmailBody` em `emails.go`.
