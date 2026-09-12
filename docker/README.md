# Docker

Infraestrutura local de desenvolvimento do projeto, via Docker Compose. Este arquivo é atualizado conforme novos serviços forem adicionados.

## Serviços

### `dovecot`

Servidor Dovecot para testes de IMAP durante o desenvolvimento.

- Imagem: `dovecot/dovecot:latest` (imagem oficial do projeto Dovecot).
- Portas: `127.0.0.1:143` → IMAP em texto plano (sem TLS); `127.0.0.1:24` → LMTP em texto plano (sem TLS), usado pelo script de `populate-mailbox/` para entregar os e-mails de teste. Conexões apenas locais, por design.
- Autenticação: o `passdb static` padrão da imagem aceita **qualquer nome de usuário**, desde que a senha seja a definida em `DOVECOT_MAILBOX_PASSWORD`. Não há uma lista fixa de caixas postais.
- Configuração:
  - `dovecot/conf.d/zz-local-plain.conf` é montado individualmente sobre `/etc/dovecot/conf.d/` para desabilitar SSL e permitir auth em texto plano (`ssl = no`, `auth_allow_cleartext = yes`). É montado como arquivo avulso — não a pasta inteira — porque a imagem já vem com seus próprios arquivos padrão em `conf.d/` (como `auth.conf`, que configura o `USER_PASSWORD`); sobrescrever a pasta toda os esconderia e quebraria a autenticação.
  - `dovecot/conf.d/zz-local-lmtp.conf` (também montado individualmente) habilita o listener LMTP em texto plano, o roteamento por pasta via `usuario+NomeDaPasta@...` (`lmtp_save_to_detail_mailbox`) e normaliza a resolução de usuário (`auth_username_format = %n`) para que LMTP e IMAP caiam na mesma caixa postal.
  - Os e-mails ficam persistidos no volume `dovecot_mail`.

## Como executar

```bash
cd docker
cp .env.example .env   # opcional: personalize DOVECOT_MAILBOX_PASSWORD
docker compose up -d
```

Testando o login (qualquer usuário, com a senha configurada):

```bash
python3 -c "
import imaplib
m = imaplib.IMAP4('127.0.0.1', 143)
print(m.login('testuser', 'changeme123'))
"
```

Para derrubar:

```bash
docker compose down
```

Para derrubar e apagar também os e-mails persistidos:

```bash
docker compose down -v
```

## Massa de e-mails de teste

Para testar o parsing de e-mails diversos (encodings, multipart, anexos e casos mal formados de propósito), há três fontes de `.eml` e um script para entregá-los na caixa de teste. Detalhes de uso em cada README.md.

### Gerando/obtendo os `.eml` (`generate-emails/`)

- `generate-emails/generate_synthetic_emails.py` — gera ~37 e-mails sintéticos cobrindo charsets diversos (ISO-8859-1, Windows-1252, Shift_JIS, GB2312, KOI8-R, ISO-2022-JP...), `Content-Transfer-Encoding` variados, multipart (alternative/mixed/related, inclusive aninhado), anexos pequenos, headers com `encoded-words` (RFC 2047), e uma boa quantidade de casos propositalmente mal formados: boundary ausente/divergente/truncado, charset mentiroso, headers duplicados/ausentes, base64/quoted-printable inválidos, filename RFC 2231, e-mail sem a linha em branco entre headers e corpo, etc.
- `generate-emails/fetch_corpus_samples.py` — baixa uma amostra (~520 e-mails, ≤20KB cada, ~2MB no total) do [SpamAssassin public corpus](https://spamassassin.apache.org/old/publiccorpus/) para complementar com diversidade "orgânica" real que seria difícil prever manualmente.

Ambos os scripts jogam a saída, sem distinção de pastas, em `generate-emails/emails-generated/` (prefixos `syn_` e `corpus_`) — cada execução limpa apenas os arquivos do seu próprio prefixo antes de gerar/baixar de novo.

```bash
python3 generate-emails/generate_synthetic_emails.py
python3 generate-emails/fetch_corpus_samples.py
```

A partir daí, a organização em `emails/<pasta>/` (ver abaixo) é manual.

### Fixtures permanentes (`emails-permanent/`)

Diferente de `emails/`, esta pasta **não é gitignored** — é versionada no git e nunca é apagada/regenerada por nenhum script. Use pra casos específicos que precisam estar sempre disponíveis, em qualquer clone do repo, sem depender de rodar os scripts de geração. Estrutura sempre plana (sem subpastas): todo `.eml` colocado direto aqui vai pra `INBOX`. Ver `emails-permanent/README.md`.

### Populando a caixa (`populate-mailbox/populate_mailbox.py`)

Lê `emails/<nome-da-pasta>/*.eml` e entrega cada arquivo, cru (sem reparsear no cliente), para a pasta IMAP de mesmo nome via **LMTP** (não `APPEND`) — `emails/INBOX` vai para a INBOX, `emails/teste` vira a pasta `teste` (criada se não existir), e assim por diante. Em seguida, entrega do mesmo jeito todo `.eml` solto em `emails-permanent/`, sempre para a INBOX. A entrega real por LMTP faz o Dovecot acrescentar cabeçalhos diversos (`Return-Path`, `Delivered-To`, `Received`) por cima do fixture, propositalmente, para mais realismo/diversidade.

Sempre que executado, **zera a caixa do usuário de teste primeiro** (remove todas as pastas exceto INBOX e esvazia a INBOX) antes de repopular — é seguro rodar quantas vezes quiser. Isso afeta só a caixa postal no Dovecot; os arquivos `.eml` em si nunca são apagados (por isso os de `emails-permanent/` sempre voltam a cada rodada).

Usuário e senha são constantes no topo do script (`MAILBOX_USER`, `MAILBOX_PASSWORD`) — como a autenticação do Dovecot usa uma única senha compartilhada (`DOVECOT_MAILBOX_PASSWORD` no `.env`), se você mudar a senha no `.env` do Dovecot, atualize `MAILBOX_PASSWORD` no script para acompanhar.

```bash
python3 populate-mailbox/populate_mailbox.py
```
