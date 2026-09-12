#!/usr/bin/env python3
"""
Gera uma massa de e-mails .eml sintéticos para testar o parser da API:
encodings diversos, transfer-encodings diversos, estruturas multipart e
uma boa quantidade de casos propositalmente mal formados (headers
inválidos, BODYSTRUCTURE quebrado, encodings mentirosos etc).

Todos os arquivos vão para uma única pasta plana (OUT_DIR) com o
prefixo "syn_" — a separação em pastas por caixa/pasta IMAP é manual.

Reexecutável: cada rodada apaga os "syn_*.eml" antigos antes de gerar
de novo (não mexe em arquivos com outro prefixo, como os do
fetch_corpus_samples.py).
"""
from __future__ import annotations

import base64
import quopri
from pathlib import Path

OUT_DIR = Path(__file__).resolve().parent / "emails-generated"

# --- pequenos anexos válidos, usados em vários casos -----------------------

TINY_TXT = b"Hello, this is a tiny plain text attachment.\n"

# PNG 1x1 transparente válido
TINY_PNG = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUB"
    "AScY42YAAAAASUVORK5CYII="
)

TINY_JSON = b'{"ok": true, "n": 1}\n'


def build_raw(headers: list[tuple[str, str | bytes]], body: bytes) -> bytes:
    """Monta um e-mail raw a partir de headers (str ou bytes) + corpo em bytes."""
    header_bytes = b""
    for key, value in headers:
        value_bytes = value.encode("utf-8") if isinstance(value, str) else value
        header_bytes += key.encode("ascii") + b": " + value_bytes + b"\r\n"
    return header_bytes + b"\r\n" + body


def base_headers(subject: str, msg_id: str) -> list[tuple[str, str]]:
    return [
        ("From", "Remetente de Teste <remetente@example.com>"),
        ("To", "destinatario@example.com"),
        ("Subject", subject),
        ("Date", "Mon, 05 Sep 2026 10:00:00 +0000"),
        ("Message-ID", f"<{msg_id}@example.com>"),
        ("MIME-Version", "1.0"),
    ]


# --- casos "bem formados", mas com diversidade de encoding -----------------

def case_plain_ascii_7bit() -> bytes:
    headers = base_headers("Plain ASCII 7bit", "syn-01") + [
        ("Content-Type", "text/plain; charset=us-ascii"),
        ("Content-Transfer-Encoding", "7bit"),
    ]
    return build_raw(headers, b"Simple plain ASCII body, nothing fancy.\r\n")


def case_plain_iso8859_1_qp() -> bytes:
    text = "Café com leite e pãozinho - ça va bien, não é?\r\n"
    headers = base_headers("Plain ISO-8859-1 quoted-printable", "syn-02") + [
        ("Content-Type", "text/plain; charset=iso-8859-1"),
        ("Content-Transfer-Encoding", "quoted-printable"),
    ]
    return build_raw(headers, quopri.encodestring(text.encode("iso-8859-1")))


def case_plain_windows1252_8bit() -> bytes:
    text = "“Smart quotes” and an em dash — in cp1252, 8bit raw.\r\n"
    headers = base_headers("Plain Windows-1252 8bit", "syn-03") + [
        ("Content-Type", "text/plain; charset=windows-1252"),
        ("Content-Transfer-Encoding", "8bit"),
    ]
    return build_raw(headers, text.encode("cp1252"))


def case_plain_shiftjis_base64() -> bytes:
    text = "こんにちは、世界！\r\n"
    headers = base_headers("Plain Shift_JIS base64", "syn-04") + [
        ("Content-Type", "text/plain; charset=shift_jis"),
        ("Content-Transfer-Encoding", "base64"),
    ]
    return build_raw(headers, base64.encodebytes(text.encode("shift_jis")))


def case_plain_gb2312_base64() -> bytes:
    text = "你好，世界！\r\n"
    headers = base_headers("Plain GB2312 base64", "syn-05") + [
        ("Content-Type", "text/plain; charset=gb2312"),
        ("Content-Transfer-Encoding", "base64"),
    ]
    return build_raw(headers, base64.encodebytes(text.encode("gb2312")))


def case_plain_koi8r_qp() -> bytes:
    text = "Привет, мир!\r\n"
    headers = base_headers("Plain KOI8-R quoted-printable", "syn-06") + [
        ("Content-Type", "text/plain; charset=koi8-r"),
        ("Content-Transfer-Encoding", "quoted-printable"),
    ]
    return build_raw(headers, quopri.encodestring(text.encode("koi8-r")))


def case_plain_iso2022jp_7bit() -> bytes:
    text = "日本語のメールです。\r\n"
    headers = base_headers("Plain ISO-2022-JP 7bit", "syn-07") + [
        ("Content-Type", "text/plain; charset=iso-2022-jp"),
        ("Content-Transfer-Encoding", "7bit"),
    ]
    return build_raw(headers, text.encode("iso2022_jp"))


def case_html_utf8_base64() -> bytes:
    html = "<html><body><p>Olá! \U0001f389 <b>UTF-8</b> HTML body.</p></body></html>\r\n"
    headers = base_headers("HTML UTF-8 base64", "syn-08") + [
        ("Content-Type", "text/html; charset=utf-8"),
        ("Content-Transfer-Encoding", "base64"),
    ]
    return build_raw(headers, base64.encodebytes(html.encode("utf-8")))


def case_html_iso8859_1_qp() -> bytes:
    html = (
        "<html><body><p>Preço: 10&euro; &mdash; é só um teste.</p>"
        "</body></html>\r\n"
    )
    headers = base_headers("HTML ISO-8859-1 quoted-printable", "syn-09") + [
        ("Content-Type", "text/html; charset=iso-8859-1"),
        ("Content-Transfer-Encoding", "quoted-printable"),
    ]
    return build_raw(headers, quopri.encodestring(html.encode("iso-8859-1")))


# --- multipart bem formados --------------------------------------------

def case_multipart_alternative_utf8() -> bytes:
    boundary = "BOUNDARY-ALT-UTF8"
    plain = "Olá mundo em texto simples.\r\n".encode("utf-8")
    html = "<html><body>Olá <b>mundo</b> em HTML.</body></html>\r\n".encode("utf-8")
    body = (
        f"--{boundary}\r\n"
        "Content-Type: text/plain; charset=utf-8\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(plain) + (
        f"--{boundary}\r\n"
        "Content-Type: text/html; charset=utf-8\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(html) + f"--{boundary}--\r\n".encode("ascii")
    headers = base_headers("Multipart alternative UTF-8", "syn-10") + [
        ("Content-Type", f'multipart/alternative; boundary="{boundary}"'),
    ]
    return build_raw(headers, body)


def case_multipart_alternative_mixed_charsets() -> bytes:
    boundary = "BOUNDARY-ALT-MIXED"
    plain = quopri.encodestring("Café em ISO-8859-1 (parte texto).\r\n".encode("iso-8859-1"))
    html = "<html><body>Café em UTF-8 (parte HTML).</body></html>\r\n".encode("utf-8")
    body = (
        f"--{boundary}\r\n"
        "Content-Type: text/plain; charset=iso-8859-1\r\n"
        "Content-Transfer-Encoding: quoted-printable\r\n\r\n"
    ).encode("ascii") + plain + (
        f"--{boundary}\r\n"
        "Content-Type: text/html; charset=utf-8\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(html) + f"--{boundary}--\r\n".encode("ascii")
    headers = base_headers("Multipart alternative, charsets diferentes por parte", "syn-11") + [
        ("Content-Type", f'multipart/alternative; boundary="{boundary}"'),
    ]
    return build_raw(headers, body)


def case_multipart_mixed_text_attachment() -> bytes:
    boundary = "BOUNDARY-MIXED-TXT"
    body_text = "Segue anexo de texto.\r\n".encode("utf-8")
    body = (
        f"--{boundary}\r\n"
        "Content-Type: text/plain; charset=utf-8\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(body_text) + (
        f"--{boundary}\r\n"
        "Content-Type: text/plain; name=\"tiny.txt\"\r\n"
        "Content-Disposition: attachment; filename=\"tiny.txt\"\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(TINY_TXT) + f"--{boundary}--\r\n".encode("ascii")
    headers = base_headers("Multipart mixed com anexo de texto", "syn-12") + [
        ("Content-Type", f'multipart/mixed; boundary="{boundary}"'),
    ]
    return build_raw(headers, body)


def case_multipart_mixed_image_attachment() -> bytes:
    boundary = "BOUNDARY-MIXED-IMG"
    html = "<html><body>Segue imagem anexada.</body></html>\r\n".encode("utf-8")
    body = (
        f"--{boundary}\r\n"
        "Content-Type: text/html; charset=utf-8\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(html) + (
        f"--{boundary}\r\n"
        "Content-Type: image/png; name=\"tiny.png\"\r\n"
        "Content-Disposition: attachment; filename=\"tiny.png\"\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(TINY_PNG) + f"--{boundary}--\r\n".encode("ascii")
    headers = base_headers("Multipart mixed com anexo de imagem", "syn-13") + [
        ("Content-Type", f'multipart/mixed; boundary="{boundary}"'),
    ]
    return build_raw(headers, body)


def case_multipart_mixed_multi_attachments() -> bytes:
    boundary = "BOUNDARY-MIXED-MULTI"
    text = "Tres anexos diferentes neste e-mail.\r\n".encode("utf-8")
    parts = [
        ("text/plain; charset=utf-8", None, text),
        ("text/plain; name=\"tiny.txt\"", "tiny.txt", TINY_TXT),
        ("image/png; name=\"tiny.png\"", "tiny.png", TINY_PNG),
        ("application/json; name=\"tiny.json\"", "tiny.json", TINY_JSON),
    ]
    body = b""
    for content_type, filename, content in parts:
        part_headers = f"--{boundary}\r\nContent-Type: {content_type}\r\n"
        if filename:
            part_headers += f'Content-Disposition: attachment; filename="{filename}"\r\n'
        part_headers += "Content-Transfer-Encoding: base64\r\n\r\n"
        body += part_headers.encode("utf-8") + base64.encodebytes(content)
    body += f"--{boundary}--\r\n".encode("ascii")
    headers = base_headers("Multipart mixed com varios anexos", "syn-14") + [
        ("Content-Type", f'multipart/mixed; boundary="{boundary}"'),
    ]
    return build_raw(headers, body)


def case_multipart_related_inline_image() -> bytes:
    boundary = "BOUNDARY-RELATED"
    html = (
        '<html><body>Logo inline: <img src="cid:logo1"></body></html>\r\n'
    ).encode("utf-8")
    body = (
        f"--{boundary}\r\n"
        "Content-Type: text/html; charset=utf-8\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(html) + (
        f"--{boundary}\r\n"
        "Content-Type: image/png\r\n"
        "Content-Transfer-Encoding: base64\r\n"
        "Content-ID: <logo1>\r\n"
        "Content-Disposition: inline; filename=\"logo.png\"\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(TINY_PNG) + f"--{boundary}--\r\n".encode("ascii")
    headers = base_headers("Multipart related com imagem inline", "syn-15") + [
        ("Content-Type", f'multipart/related; boundary="{boundary}"; type="text/html"'),
    ]
    return build_raw(headers, body)


def case_nested_multipart() -> bytes:
    rel_boundary = "BOUNDARY-NESTED-RELATED"
    alt_boundary = "BOUNDARY-NESTED-ALT"
    mixed_boundary = "BOUNDARY-NESTED-MIXED"

    html = '<html><body>HTML com <img src="cid:logo2"></body></html>\r\n'.encode("utf-8")
    related = (
        f"--{rel_boundary}\r\n"
        "Content-Type: text/html; charset=utf-8\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(html) + (
        f"--{rel_boundary}\r\n"
        "Content-Type: image/png\r\n"
        "Content-ID: <logo2>\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(TINY_PNG) + f"--{rel_boundary}--\r\n".encode("ascii")

    plain = "Versao em texto simples do e-mail aninhado.\r\n".encode("utf-8")
    alternative = (
        f"--{alt_boundary}\r\n"
        "Content-Type: text/plain; charset=utf-8\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(plain) + (
        f"--{alt_boundary}\r\n"
        f'Content-Type: multipart/related; boundary="{rel_boundary}"\r\n\r\n'
    ).encode("ascii") + related + f"--{alt_boundary}--\r\n".encode("ascii")

    mixed = (
        f"--{mixed_boundary}\r\n"
        f'Content-Type: multipart/alternative; boundary="{alt_boundary}"\r\n\r\n'
    ).encode("ascii") + alternative + (
        f"--{mixed_boundary}\r\n"
        "Content-Type: text/plain; name=\"tiny.txt\"\r\n"
        "Content-Disposition: attachment; filename=\"tiny.txt\"\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(TINY_TXT) + f"--{mixed_boundary}--\r\n".encode("ascii")

    headers = base_headers("Multipart aninhado: mixed > alternative > related", "syn-16") + [
        ("Content-Type", f'multipart/mixed; boundary="{mixed_boundary}"'),
    ]
    return build_raw(headers, mixed)


# --- headers com encoded-words (RFC 2047) -------------------------------

def case_subject_encoded_word_valid_utf8() -> bytes:
    encoded = "=?UTF-8?B?" + base64.b64encode("Relatório de vendas 📊".encode("utf-8")).decode() + "?="
    headers = base_headers(encoded, "syn-17") + [
        ("Content-Type", "text/plain; charset=us-ascii"),
        ("Content-Transfer-Encoding", "7bit"),
    ]
    return build_raw(headers, b"Body simples, o interessante esta no Subject.\r\n")


def case_subject_encoded_word_valid_iso2022jp() -> bytes:
    payload = "件名：日本語".encode("iso2022_jp")
    encoded = "=?ISO-2022-JP?B?" + base64.b64encode(payload).decode() + "?="
    headers = base_headers(encoded, "syn-18") + [
        ("Content-Type", "text/plain; charset=us-ascii"),
        ("Content-Transfer-Encoding", "7bit"),
    ]
    return build_raw(headers, b"Subject encoded word valido em ISO-2022-JP.\r\n")


def case_from_display_name_encoded_word() -> bytes:
    display = "=?UTF-8?Q?Jos=C3=A9_Ant=C3=B4nio?="
    headers = [
        ("From", f"{display} <jose@example.com>"),
        ("To", "destinatario@example.com"),
        ("Subject", "From com encoded-word no display name"),
        ("Date", "Mon, 05 Sep 2026 10:00:00 +0000"),
        ("Message-ID", "<syn-19@example.com>"),
        ("MIME-Version", "1.0"),
        ("Content-Type", "text/plain; charset=us-ascii"),
        ("Content-Transfer-Encoding", "7bit"),
    ]
    return build_raw(headers, b"Testa decodificacao de display name.\r\n")


def case_subject_encoded_word_malformed() -> bytes:
    # charset inexistente + encoded-word sem fechamento "?="
    headers = base_headers("=?ZZZZ-INVALID?B?w6nDp8Ojw6M", "syn-20") + [
        ("Content-Type", "text/plain; charset=us-ascii"),
        ("Content-Transfer-Encoding", "7bit"),
    ]
    return build_raw(headers, b"Subject com encoded-word malformado de proposito.\r\n")


# --- casos deliberadamente mal formados ---------------------------------

def case_charset_mismatch_utf8_actual_latin1() -> bytes:
    # Content-Type diz utf-8, mas o corpo tem bytes latin-1 que nao sao utf-8 valido
    body = "Café com pastel\r\n".encode("latin-1")
    headers = base_headers("Charset mentiroso: declara UTF-8, corpo e Latin-1", "syn-21") + [
        ("Content-Type", "text/plain; charset=utf-8"),
        ("Content-Transfer-Encoding", "8bit"),
    ]
    return build_raw(headers, body)


def case_missing_charset_nonascii_body() -> bytes:
    body = "Sem charset declarado, mas com acentos: ção\r\n".encode("utf-8")
    headers = base_headers("Content-Type sem charset, corpo nao-ascii", "syn-22") + [
        ("Content-Type", "text/plain"),
        ("Content-Transfer-Encoding", "8bit"),
    ]
    return build_raw(headers, body)


def case_multipart_missing_boundary_param() -> bytes:
    body = (
        b"--somelabel\r\n"
        b"Content-Type: text/plain\r\n\r\n"
        b"Parte 1\r\n"
        b"--somelabel\r\n"
        b"Content-Type: text/plain\r\n\r\n"
        b"Parte 2\r\n"
        b"--somelabel--\r\n"
    )
    headers = base_headers("Multipart sem parametro boundary no Content-Type", "syn-23") + [
        ("Content-Type", "multipart/mixed"),
    ]
    return build_raw(headers, body)


def case_multipart_boundary_mismatch() -> bytes:
    # Content-Type declara "ABC123", mas o corpo usa "XYZ999"
    body = (
        b"--XYZ999\r\n"
        b"Content-Type: text/plain\r\n\r\n"
        b"Parte com boundary divergente do header.\r\n"
        b"--XYZ999--\r\n"
    )
    headers = base_headers("Boundary do body diferente do declarado no header", "syn-24") + [
        ("Content-Type", 'multipart/mixed; boundary="ABC123"'),
    ]
    return build_raw(headers, body)


def case_multipart_truncated_no_closing_boundary() -> bytes:
    boundary = "BOUNDARY-TRUNCATED"
    body = (
        f"--{boundary}\r\n"
        "Content-Type: text/plain\r\n\r\n"
        "Parte 1, ok.\r\n"
        f"--{boundary}\r\n"
        "Content-Type: text/plain\r\n\r\n"
        "Parte 2, mas o e-mail termina aqui sem o boundary final.\r\n"
    ).encode("utf-8")
    headers = base_headers("Multipart truncado, sem boundary de fechamento", "syn-25") + [
        ("Content-Type", f'multipart/mixed; boundary="{boundary}"'),
    ]
    return build_raw(headers, body)


def case_duplicate_headers() -> bytes:
    headers = [
        ("From", "primeiro@example.com"),
        ("From", "segundo@example.com"),
        ("To", "destinatario@example.com"),
        ("Subject", "Primeiro assunto"),
        ("Subject", "Segundo assunto (duplicado)"),
        ("Date", "Mon, 05 Sep 2026 10:00:00 +0000"),
        ("Message-ID", "<syn-26@example.com>"),
        ("MIME-Version", "1.0"),
        ("Content-Type", "text/plain; charset=us-ascii"),
        ("Content-Transfer-Encoding", "7bit"),
    ]
    return build_raw(headers, b"E-mail com headers From/Subject duplicados.\r\n")


def case_missing_from_header() -> bytes:
    headers = [
        ("To", "destinatario@example.com"),
        ("Subject", "Sem header From"),
        ("Date", "Mon, 05 Sep 2026 10:00:00 +0000"),
        ("Message-ID", "<syn-27@example.com>"),
        ("MIME-Version", "1.0"),
        ("Content-Type", "text/plain; charset=us-ascii"),
        ("Content-Transfer-Encoding", "7bit"),
    ]
    return build_raw(headers, b"Este e-mail nao tem header From.\r\n")


def case_malformed_date_header() -> bytes:
    headers = [
        ("From", "remetente@example.com"),
        ("To", "destinatario@example.com"),
        ("Subject", "Date invalido"),
        ("Date", "isso nao e uma data valida"),
        ("Message-ID", "<syn-28@example.com>"),
        ("MIME-Version", "1.0"),
        ("Content-Type", "text/plain; charset=us-ascii"),
        ("Content-Transfer-Encoding", "7bit"),
    ]
    return build_raw(headers, b"Header Date propositalmente invalido.\r\n")


def case_extremely_long_header_line() -> bytes:
    long_subject = "Assunto muito longo sem folding - " + ("x" * 1200)
    headers = base_headers(long_subject, "syn-29") + [
        ("Content-Type", "text/plain; charset=us-ascii"),
        ("Content-Transfer-Encoding", "7bit"),
    ]
    return build_raw(headers, b"Testa uma unica linha de header extremamente longa.\r\n")


def case_mixed_line_endings() -> bytes:
    # headers com CRLF, corpo com mistura de LF puro e CRLF
    headers = base_headers("Mistura de terminadores de linha (CRLF/LF)", "syn-30")
    headers.append(("Content-Type", "text/plain; charset=us-ascii"))
    headers.append(("Content-Transfer-Encoding", "7bit"))
    body = b"Linha 1 com CRLF\r\nLinha 2 so com LF\nLinha 3 com CRLF de novo\r\n"
    return build_raw(headers, body)


def case_invalid_base64_body() -> bytes:
    headers = base_headers("CTE base64 mas corpo nao e base64 valido", "syn-31") + [
        ("Content-Type", "text/plain; charset=utf-8"),
        ("Content-Transfer-Encoding", "base64"),
    ]
    return build_raw(headers, b"Isto n@o - eh base64 valido!!! ====\r\n")


def case_invalid_quoted_printable_body() -> bytes:
    headers = base_headers("CTE quoted-printable com sequencias invalidas", "syn-32") + [
        ("Content-Type", "text/plain; charset=utf-8"),
        ("Content-Transfer-Encoding", "quoted-printable"),
    ]
    return build_raw(headers, b"Sequencia invalida aqui: =ZZ e tambem =\r\n solta no fim=\r\n")


def case_unbalanced_quotes_content_type() -> bytes:
    headers = base_headers("Content-Type com aspas desbalanceadas", "syn-33") + [
        ("Content-Type", 'text/plain; charset="utf-8'),  # falta a aspa de fechamento
        ("Content-Transfer-Encoding", "7bit"),
    ]
    return build_raw(headers, b"Corpo simples, o problema esta no header acima.\r\n")


def case_rfc2231_encoded_filename() -> bytes:
    boundary = "BOUNDARY-RFC2231"
    body = (
        f"--{boundary}\r\n"
        "Content-Type: text/plain; charset=utf-8\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes("Anexo com nome de arquivo com acento/simbolo.\r\n".encode("utf-8")) + (
        f"--{boundary}\r\n"
        "Content-Type: text/plain\r\n"
        "Content-Disposition: attachment; filename*=UTF-8''%E2%82%AC%20relat%C3%B3rio.txt\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(TINY_TXT) + f"--{boundary}--\r\n".encode("ascii")
    headers = base_headers("Anexo com filename RFC 2231 (UTF-8 percent-encoded)", "syn-34") + [
        ("Content-Type", f'multipart/mixed; boundary="{boundary}"'),
    ]
    return build_raw(headers, body)


def case_attachment_no_filename_octet_stream() -> bytes:
    boundary = "BOUNDARY-NO-FILENAME"
    body = (
        f"--{boundary}\r\n"
        "Content-Type: text/plain; charset=utf-8\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes("Anexo sem nome nenhum a seguir.\r\n".encode("utf-8")) + (
        f"--{boundary}\r\n"
        "Content-Type: application/octet-stream\r\n"
        "Content-Transfer-Encoding: base64\r\n\r\n"
    ).encode("ascii") + base64.encodebytes(TINY_JSON) + f"--{boundary}--\r\n".encode("ascii")
    headers = base_headers("Anexo sem filename e sem Content-Disposition", "syn-35") + [
        ("Content-Type", f'multipart/mixed; boundary="{boundary}"'),
    ]
    return build_raw(headers, body)


def case_empty_body() -> bytes:
    headers = base_headers("Corpo vazio", "syn-36") + [
        ("Content-Type", "text/plain; charset=us-ascii"),
        ("Content-Transfer-Encoding", "7bit"),
    ]
    return build_raw(headers, b"")


def case_no_body_separator() -> bytes:
    # Falta a linha em branco entre headers e corpo (malformado de proposito)
    raw = (
        "From: remetente@example.com\r\n"
        "To: destinatario@example.com\r\n"
        "Subject: Sem linha em branco separando headers do corpo\r\n"
        "Date: Mon, 05 Sep 2026 10:00:00 +0000\r\n"
        "Message-ID: <syn-37@example.com>\r\n"
        "Content-Type: text/plain; charset=us-ascii\r\n"
        "Isto deveria ser o corpo, mas nao ha linha em branco antes.\r\n"
    ).encode("utf-8")
    return raw


CASES: list[tuple[str, callable]] = [
    ("syn_01_plain_ascii_7bit", case_plain_ascii_7bit),
    ("syn_02_plain_iso8859-1_qp", case_plain_iso8859_1_qp),
    ("syn_03_plain_windows1252_8bit", case_plain_windows1252_8bit),
    ("syn_04_plain_shiftjis_base64", case_plain_shiftjis_base64),
    ("syn_05_plain_gb2312_base64", case_plain_gb2312_base64),
    ("syn_06_plain_koi8r_qp", case_plain_koi8r_qp),
    ("syn_07_plain_iso2022jp_7bit", case_plain_iso2022jp_7bit),
    ("syn_08_html_utf8_base64", case_html_utf8_base64),
    ("syn_09_html_iso8859-1_qp", case_html_iso8859_1_qp),
    ("syn_10_multipart_alternative_utf8", case_multipart_alternative_utf8),
    ("syn_11_multipart_alternative_mixed_charsets", case_multipart_alternative_mixed_charsets),
    ("syn_12_multipart_mixed_text_attachment", case_multipart_mixed_text_attachment),
    ("syn_13_multipart_mixed_image_attachment", case_multipart_mixed_image_attachment),
    ("syn_14_multipart_mixed_multi_attachments", case_multipart_mixed_multi_attachments),
    ("syn_15_multipart_related_inline_image", case_multipart_related_inline_image),
    ("syn_16_nested_multipart", case_nested_multipart),
    ("syn_17_subject_encoded_word_valid_utf8", case_subject_encoded_word_valid_utf8),
    ("syn_18_subject_encoded_word_valid_iso2022jp", case_subject_encoded_word_valid_iso2022jp),
    ("syn_19_from_display_name_encoded_word", case_from_display_name_encoded_word),
    ("syn_20_subject_encoded_word_malformed", case_subject_encoded_word_malformed),
    ("syn_21_charset_mismatch_utf8_actual_latin1", case_charset_mismatch_utf8_actual_latin1),
    ("syn_22_missing_charset_nonascii_body", case_missing_charset_nonascii_body),
    ("syn_23_multipart_missing_boundary_param", case_multipart_missing_boundary_param),
    ("syn_24_multipart_boundary_mismatch", case_multipart_boundary_mismatch),
    ("syn_25_multipart_truncated_no_closing_boundary", case_multipart_truncated_no_closing_boundary),
    ("syn_26_duplicate_headers", case_duplicate_headers),
    ("syn_27_missing_from_header", case_missing_from_header),
    ("syn_28_malformed_date_header", case_malformed_date_header),
    ("syn_29_extremely_long_header_line", case_extremely_long_header_line),
    ("syn_30_mixed_line_endings", case_mixed_line_endings),
    ("syn_31_invalid_base64_body", case_invalid_base64_body),
    ("syn_32_invalid_quoted_printable_body", case_invalid_quoted_printable_body),
    ("syn_33_unbalanced_quotes_content_type", case_unbalanced_quotes_content_type),
    ("syn_34_rfc2231_encoded_filename", case_rfc2231_encoded_filename),
    ("syn_35_attachment_no_filename_octet_stream", case_attachment_no_filename_octet_stream),
    ("syn_36_empty_body", case_empty_body),
    ("syn_37_no_body_separator", case_no_body_separator),
]


def main() -> None:
    OUT_DIR.mkdir(parents=True, exist_ok=True)

    for old in OUT_DIR.glob("syn_*.eml"):
        old.unlink()

    for name, builder in CASES:
        raw = builder()
        out_path = OUT_DIR / f"{name}.eml"
        out_path.write_bytes(raw)

    print(f"{len(CASES)} e-mails sinteticos gerados em {OUT_DIR}")


if __name__ == "__main__":
    main()
