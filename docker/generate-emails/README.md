# generate-emails

Gera o conjunto bruto de fixtures `.eml` usado para testar o parser IMAP da futura API: uma mistura deliberadamente diversa e "traiçoeira" de charsets, transfer encodings, formatos multipart e cabeçalhos/BODYSTRUCTURE mal formados, sem precisar de corpos grandes ou anexos reais.

Os dois scripts escrevem na mesma pasta plana `emails-generated/` (gitignored, dentro desta pasta) — esse é só o material bruto; a curadoria manual em pastas por caixa IMAP acontece depois, em `../emails/` (ver README de `../populate-mailbox/`).

## Scripts

### `generate_synthetic_emails.py`

Casos construídos à mão, tanto de diversidade válida (charsets, encodings, multipart/nested, encoded-words no Subject/From) quanto de malformação deliberada (headers duplicados, boundary ausente, base64/quoted-printable inválido, sem linha em branco separando headers do corpo, etc.). Cada caso vira um arquivo `syn_NN_descricao.eml`.

Adicione novos casos aqui conforme os requisitos de tratamento de erro do parser forem ficando mais claros.

Executar:

```bash
python3 generate_synthetic_emails.py
```

Reexecutável: apaga os `syn_*.eml` antigos antes de gerar de novo (não mexe nos `corpus_*.eml`).

### `fetch_corpus_samples.py`

Baixa uma amostra do [SpamAssassin public corpus](https://spamassassin.apache.org/old/publiccorpus/) (ham + spam de 2003) para complementar os casos sintéticos com diversidade "orgânica" real, difícil de prever manualmente. Filtra por um teto de tamanho por e-mail (`MAX_SIZE_BYTES`) e usa uma seed fixa (`SEED`) para a amostragem, então o resultado é determinístico entre execuções. As contagens por categoria (`CORPORA`) estão calibradas para ~2MB no total.

**Quirk do corpus:** apesar de distribuído um arquivo por mensagem, cada arquivo do corpus mantém a linha separadora de mbox (`From remetente Weekday Mon DD HH:MM:SS YYYY`) como se fosse a primeira linha do e-mail — não é um header RFC 822 válido (sem `:`) e nenhuma entrega real (SMTP/LMTP/IMAP APPEND) a produziria, então não é uma malformação "de verdade" que o parser precise tolerar, só um artefato de empacotamento do corpus. O script remove essa linha (`strip_mbox_from_line`) antes de salvar cada `.eml`.

Executar:

```bash
python3 fetch_corpus_samples.py
```

Requer acesso à internet. Reexecutável: apaga os `corpus_*.eml` antigos antes de baixar de novo (não mexe nos `syn_*.eml`).
