#!/usr/bin/env python3
"""
Zera a caixa postal de teste no Dovecot local e a repopula a partir de
duas fontes de .eml:

- docker/emails/<pasta>/*.eml — cada subpasta vira uma pasta IMAP de
  mesmo nome (docker/emails/INBOX -> INBOX, docker/emails/teste ->
  teste, criada se nao existir). Gitignored, regeneravel a partir de
  ../generate-emails/ (ver README dali).
- docker/emails-permanent/*.eml — fixtures manuais permanentes,
  versionadas no git (nunca apagadas, ao contrario da pasta acima).
  Estrutura sempre plana (sem subpastas): todo arquivo aqui vai pra
  INBOX.

Os .eml sao entregues crus (bytes exatos do arquivo, sem reparsear/
re-serializar no cliente) via LMTP — como uma entrega de e-mail real
faria — para que o Dovecot acrescente cabecalhos de entrega diversos
(Return-Path, Delivered-To, Received) por cima dos fixtures, inclusive
os mal formados. O roteamento pra pasta correta usa o "+detail" do
Dovecot (lmtp_save_to_detail_mailbox, ver docker/dovecot/conf.d/
zz-local-lmtp.conf): a pasta precisa existir antes da entrega (por isso
ainda criamos as pastas via IMAP), senao a mensagem cai na INBOX.

Reexecutavel: cada rodada apaga TODAS as pastas/mensagens existentes do
usuario de teste antes de repopular. Isso afeta so a caixa postal no
Dovecot — os arquivos .eml em si (inclusive os de emails-permanent/)
nunca sao tocados por este script.
"""
from __future__ import annotations

import imaplib
import re
import smtplib
import sys
from pathlib import Path

MAILBOX_USER = "fabio"
# Dovecot's default auth (passdb static) uses ONE shared password for every
# username (see docker/.env's DOVECOT_MAILBOX_PASSWORD). Keep this in sync
# with that value or login will fail.
MAILBOX_PASSWORD = "123"
IMAP_HOST = "127.0.0.1"
IMAP_PORT = 143
LMTP_HOST = "127.0.0.1"
LMTP_PORT = 24

# Dominio usado so para montar os enderecos de entrega LMTP (RCPT TO /
# MAIL FROM) — nao ha resolucao de dominio real aqui. zz-local-lmtp.conf
# usa auth_username_format = %n pra descartar esse dominio e resolver o
# usuario so pela parte local (ex.: "testuser"), unificando com o login
# IMAP (que nao usa dominio nenhum).
RECIPIENT_DOMAIN = "example.com"
ENVELOPE_SENDER = f"populate-mailbox@{RECIPIENT_DOMAIN}"

EMAILS_DIR = Path(__file__).resolve().parent.parent / "emails"
# Fixtures manuais permanentes: versionadas no git (ao contrario de
# EMAILS_DIR, que e gitignored e regeneravel), nunca apagadas por este
# script. Estrutura sempre plana (sem subpastas) -- todo .eml aqui vai
# pra INBOX (ver docker/emails-permanent/README.md).
EMAILS_PERMANENT_DIR = Path(__file__).resolve().parent.parent / "emails-permanent"

_LIST_LINE_RE = re.compile(r'^\((?P<flags>[^)]*)\)\s+"(?P<delim>.*)"\s+(?P<name>.*)$')


def parse_list_name(entry: bytes) -> str:
    decoded = entry.decode("utf-8", errors="replace")
    match = _LIST_LINE_RE.match(decoded)
    name = match.group("name") if match else decoded.rsplit(" ", 1)[-1]
    return name.strip().strip('"')


def wipe_mailbox(imap: imaplib.IMAP4) -> None:
    typ, folders = imap.list()
    if typ != "OK" or not folders:
        return

    names = [parse_list_name(f) for f in folders if f]

    for name in names:
        if name.upper() == "INBOX":
            continue
        try:
            imap.select(name)
            imap.close()
        except imaplib.IMAP4.error:
            pass
        try:
            imap.delete(name)
        except imaplib.IMAP4.error as exc:
            print(f"  aviso: nao consegui apagar a pasta {name!r}: {exc}")

    imap.select("INBOX")
    typ, data = imap.search(None, "ALL")
    if typ == "OK" and data and data[0]:
        for num in data[0].split():
            imap.store(num, "+FLAGS", r"(\Deleted)")
        imap.expunge()
    imap.close()

    print("Caixa zerada (todas as pastas exceto INBOX removidas, INBOX esvaziada).")


def recipient_for(folder_name: str) -> str:
    if folder_name == "INBOX":
        return f"{MAILBOX_USER}@{RECIPIENT_DOMAIN}"
    return f"{MAILBOX_USER}+{folder_name}@{RECIPIENT_DOMAIN}"


def deliver_eml_files(
    imap: imaplib.IMAP4,
    lmtp: smtplib.LMTP,
    folder_name: str,
    eml_files: list[Path],
    label: str,
) -> int:
    if folder_name != "INBOX":
        # Precisa existir ANTES da entrega LMTP: lmtp_save_to_detail_mailbox
        # so roteia pro "+detail" se a pasta ja existir, senao cai na INBOX.
        typ, resp = imap.create(folder_name)
        if typ != "OK":
            print(f"  aviso: nao consegui criar a pasta {folder_name!r}: {resp}")

    recipient = recipient_for(folder_name)
    count = 0
    for eml_path in eml_files:
        raw = eml_path.read_bytes()
        try:
            lmtp.sendmail(ENVELOPE_SENDER, [recipient], raw)
        except smtplib.SMTPException as exc:
            print(f"  aviso: falha ao entregar {eml_path.name} para {folder_name!r}: {exc}")
            continue
        count += 1

    print(f"  {label}: {count} e-mail(s) entregues")
    return count


def populate(imap: imaplib.IMAP4, lmtp: smtplib.LMTP) -> None:
    total = 0

    if EMAILS_DIR.is_dir():
        for entry in sorted(EMAILS_DIR.iterdir()):
            if not entry.is_dir():
                continue

            folder_name = "INBOX" if entry.name.upper() == "INBOX" else entry.name
            eml_files = sorted(entry.rglob("*.eml"))
            if not eml_files:
                continue

            total += deliver_eml_files(imap, lmtp, folder_name, eml_files, folder_name)
    else:
        print(f"Pasta {EMAILS_DIR} nao existe, nada para popular dali.")

    if EMAILS_PERMANENT_DIR.is_dir():
        # Sempre plana: so o nivel raiz, sem rglob -- eventuais
        # subpastas aqui nao fazem parte da convencao e sao ignoradas.
        eml_files = sorted(EMAILS_PERMANENT_DIR.glob("*.eml"))
        if eml_files:
            total += deliver_eml_files(imap, lmtp, "INBOX", eml_files, "INBOX (emails-permanent)")
    else:
        print(f"Pasta {EMAILS_PERMANENT_DIR} nao existe, nada para popular dali.")

    print(f"Total de {total} e-mail(s) entregues a partir de {EMAILS_DIR} e {EMAILS_PERMANENT_DIR}")


def main() -> int:
    imap = imaplib.IMAP4(IMAP_HOST, IMAP_PORT)
    try:
        imap.login(MAILBOX_USER, MAILBOX_PASSWORD)
    except imaplib.IMAP4.error as exc:
        print(f"Falha no login IMAP: {exc}", file=sys.stderr)
        return 1

    try:
        lmtp = smtplib.LMTP()
        lmtp.connect(LMTP_HOST, LMTP_PORT)
    except OSError as exc:
        print(f"Falha na conexao LMTP: {exc}", file=sys.stderr)
        imap.logout()
        return 1

    try:
        wipe_mailbox(imap)
        populate(imap, lmtp)
    finally:
        try:
            lmtp.quit()
        except smtplib.SMTPException:
            pass
        try:
            imap.logout()
        except imaplib.IMAP4.error:
            pass

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
