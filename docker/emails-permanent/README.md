# emails-permanent

Fixtures `.eml` manuais e **permanentes**: adicione arquivos aqui à mão sempre que precisar de um caso específico garantido no ambiente local — diferente de `../emails/`, esta pasta **não é gitignored** (é versionada no git) e nunca é apagada/regenerada por nenhum script.

## Convenção

- Sempre plana: coloque os `.eml` direto aqui, sem subpastas (`emails-permanent/caso-especifico.eml`) — eventuais subpastas são ignoradas pelo `populate_mailbox.py`.
- Todo arquivo aqui vai pra **INBOX** por padrão — não há como direcionar pra outra pasta IMAP a partir daqui (use `../emails/<pasta>/` pra isso).
- Entregue via LMTP, igual aos fixtures de `../emails/` — ver `../populate-mailbox/README.md`.

## Por que não em `../emails/`?

`../emails/` é gitignored e pensada pra ser regenerada/reorganizada livremente a partir do material bruto de `../generate-emails/` — nada lá é garantido entre máquinas/clones. Esta pasta existe pra fixtures que precisam sobreviver a isso: casos que você quer que qualquer pessoa (ou CI, futuramente) tenha disponíveis só por clonar o repo, sem depender de rodar os scripts de geração.
