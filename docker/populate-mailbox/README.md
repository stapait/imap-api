# populate-mailbox

Popula a caixa postal de teste no Dovecot local a partir de dois conjuntos de fixtures `.eml`:

- `../emails/<pasta>/*.eml` — organizados manualmente, cada subpasta vira uma pasta IMAP de mesmo nome (`../emails/INBOX` -> `INBOX`, `../emails/Trabalho` -> `Trabalho`, criada se não existir). Gitignored e curada à mão a partir do material bruto gerado em `../generate-emails/emails-generated/` (ver README de `../generate-emails/`).
- `../emails-permanent/*.eml` — fixtures manuais **permanentes**, versionadas no git (nunca apagadas/regeneradas). Sempre plana (sem subpastas): todo arquivo vai pra `INBOX`. Ver `../emails-permanent/README.md`.

Este script não gera fixtures, só entrega os que já estão organizados nessas duas pastas.

## Script

### `populate_mailbox.py`

Cada execução:

1. Faz login IMAP e **apaga tudo** do usuário de teste: remove todas as pastas exceto INBOX, e esvazia a INBOX. Isso afeta só a caixa postal no Dovecot — os arquivos `.eml` em si nunca são tocados por este script (inclusive os de `../emails-permanent/`, que por isso nunca somem entre execuções).
2. Para cada subpasta de `../emails/`, cria a pasta IMAP correspondente (se ainda não existir) e entrega cada `.eml` via **LMTP** (não IMAP `APPEND`) — os bytes do arquivo vão crus, sem reparsear/re-serializar no cliente, mas a entrega real por LMTP faz o Dovecot acrescentar cabeçalhos diversos (`Return-Path`, `Delivered-To`, `Received`) por cima do fixture, propositalmente, para mais realismo/diversidade.
3. Em seguida, entrega (do mesmo jeito, via LMTP) todo `.eml` solto em `../emails-permanent/` direto pra `INBOX`.
4. O roteamento pra pasta correta usa o endereço `testuser+NomeDaPasta@...` (recurso `+detail` do Dovecot) — por isso a pasta precisa existir antes da entrega, senão a mensagem cai na INBOX.

Executar (com o Dovecot local rodando via `docker compose up -d` em `../`):

```bash
python3 populate_mailbox.py
```

Reexecutável: cada rodada zera tudo antes de repopular. `MAILBOX_USER`/`MAILBOX_PASSWORD` são constantes no topo do script, independentes do `../.env` — se você mudar `DOVECOT_MAILBOX_PASSWORD`, atualize `MAILBOX_PASSWORD` aqui também (o auth padrão do Dovecot usa uma senha única compartilhada por qualquer username).
