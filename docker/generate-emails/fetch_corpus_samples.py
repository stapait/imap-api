#!/usr/bin/env python3
"""
Baixa uma pequena amostra de e-mails reais do SpamAssassin public corpus
(https://spamassassin.apache.org/old/publiccorpus/) para complementar os
e-mails sinteticos com diversidade "organica" de charset/estrutura que
seria dificil prever manualmente.

Os arquivos vao para a mesma pasta plana do gerador sintetico
(emails-generated/), com o prefixo "corpus_" — a separacao em pastas
por caixa/pasta IMAP e manual.

Reexecutavel: cada rodada apaga os "corpus_*.eml" antigos antes de baixar
de novo (nao mexe nos arquivos "syn_*.eml").
"""
from __future__ import annotations

import random
import tarfile
import urllib.request
from pathlib import Path
from tempfile import TemporaryDirectory

OUT_DIR = Path(__file__).resolve().parent / "emails-generated"

# (URL, rotulo, quantos exemplos pegar dali)
# Contagens calibradas para ~1MB por categoria (~2MB no total), com base no
# tamanho medio real de cada corpus abaixo do teto de MAX_SIZE_BYTES: ham
# ~3.27KB/msg (2490 candidatos disponiveis), spam ~5.10KB/msg (481
# candidatos disponiveis).
CORPORA = [
    ("https://spamassassin.apache.org/old/publiccorpus/20030228_easy_ham.tar.bz2", "ham", 320),
    ("https://spamassassin.apache.org/old/publiccorpus/20030228_spam.tar.bz2", "spam", 200),
]

# Corpus real tem e-mails de tamanhos variados; como nao precisamos de
# corpo grande, filtramos por um teto de tamanho.
MAX_SIZE_BYTES = 20_000
SEED = 42
TIMEOUT_SECONDS = 60


def strip_mbox_from_line(raw: bytes) -> bytes:
    """
    O corpus do SpamAssassin, mesmo distribuido um arquivo por
    mensagem, mantem em cada arquivo a linha separadora de mbox
    ("From remetente Weekday Mon DD HH:MM:SS YYYY") como se fosse a
    primeira linha do e-mail. Essa linha nao e um header RFC 822
    valido (sem ":") e nenhuma entrega real (SMTP/LMTP/IMAP APPEND)
    a produziria — e um artefato de empacotamento do corpus, nao uma
    malformacao real de e-mail. Sem remover, o parser trata como
    header malformado fatal (o header block inteiro falha).
    """
    if raw.startswith(b"From "):
        newline = raw.find(b"\n")
        if newline != -1:
            return raw[newline + 1 :]
    return raw


def download(url: str, dest: Path) -> None:
    print(f"Baixando {url} ...")
    with urllib.request.urlopen(url, timeout=TIMEOUT_SECONDS) as response:
        dest.write_bytes(response.read())


def extract_candidates(tar_path: Path, extract_dir: Path) -> list[Path]:
    with tarfile.open(tar_path, "r:bz2") as tar:
        tar.extractall(extract_dir, filter="data")

    candidates = []
    for path in extract_dir.rglob("*"):
        if not path.is_file():
            continue
        if path.name == "cmds":
            continue
        if path.stat().st_size == 0 or path.stat().st_size > MAX_SIZE_BYTES:
            continue
        candidates.append(path)
    return candidates


def main() -> None:
    OUT_DIR.mkdir(parents=True, exist_ok=True)

    for old in OUT_DIR.glob("corpus_*.eml"):
        old.unlink()

    rng = random.Random(SEED)
    total_written = 0

    with TemporaryDirectory() as tmp:
        tmp_path = Path(tmp)
        for url, label, count in CORPORA:
            tar_path = tmp_path / f"{label}.tar.bz2"
            download(url, tar_path)

            extract_dir = tmp_path / f"{label}_extracted"
            candidates = extract_candidates(tar_path, extract_dir)
            if not candidates:
                print(f"Nenhum candidato valido encontrado para '{label}', pulando.")
                continue

            sample = rng.sample(candidates, k=min(count, len(candidates)))
            for i, path in enumerate(sorted(sample), start=1):
                raw = strip_mbox_from_line(path.read_bytes())
                out_path = OUT_DIR / f"corpus_{label}_{i:03d}.eml"
                out_path.write_bytes(raw)
                total_written += 1

    print(f"{total_written} e-mails do corpus publico salvos em {OUT_DIR}")


if __name__ == "__main__":
    main()
