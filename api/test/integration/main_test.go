//go:build integration

// Package integration contém os testes de integração da API — um por
// endpoint, exercitando o servidor HTTP real por cima do Dovecot local.
//
// Roda com `task test:integration` (ver api/Taskfile.yml), que por sua
// vez chama `go test -tags=integration ./test/integration/...`. A tag
// de build "integration" existe pra esses testes não rodarem sem
// querer num `go test ./...` comum — eles são lentos e dependem do
// Dovecot local estar de pé.
package integration

import (
	"fmt"
	"io/fs"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"imap-api/internal/config"
	"imap-api/internal/httpapi"
)

const (
	testIMAPUser = "testuser"
	// Precisa bater com docker/.env (DOVECOT_MAILBOX_PASSWORD) e com
	// MAILBOX_PASSWORD em docker/populate-mailbox/populate_mailbox.py.
	testIMAPPassword = "123"

	imapDialTimeout = 3 * time.Second
)

// server é o servidor HTTP de teste, compartilhado por todos os
// testes deste pacote — inicializado uma vez em TestMain.
var server *httptest.Server

// cfg é a configuração de teste (aponta pro Dovecot local) — exposta
// pra testes que precisam falar direto com o IMAP (ex: criar/apagar
// pastas auxiliares via imapx.WithClient), sem passar pelo HTTP.
var cfg *config.Config

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// run existe separado de TestMain porque os.Exit não roda defers — e
// aqui precisamos garantir o server.Close() mesmo se m.Run() falhar.
func run(m *testing.M) int {
	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	cfg, err = loadConfig(repoRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if err := checkIMAPReachable(cfg.IMAP.Addr()); err != nil {
		fmt.Fprintf(os.Stderr, "\nDovecot não parece estar rodando em %s: %v\n\n", cfg.IMAP.Addr(), err)
		fmt.Fprintln(os.Stderr, "Suba o ambiente local antes de rodar os testes de integração:")
		fmt.Fprintln(os.Stderr, "  cd docker && docker compose up -d")
		return 1
	}

	if err := populateMailbox(repoRoot); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	server = httptest.NewServer(httpapi.NewServer(cfg))
	defer server.Close()

	return m.Run()
}

// findRepoRoot localiza a raiz do repositório a partir do caminho
// deste próprio arquivo de teste (independente do diretório de onde
// `go test`/`task` foi chamado).
func findRepoRoot() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("não foi possível determinar o caminho deste arquivo de teste")
	}
	// este arquivo: <repo>/api/test/integration/main_test.go
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Clean(repoRoot), nil
}

func loadConfig(repoRoot string) (*config.Config, error) {
	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		path = filepath.Join(repoRoot, "api", "config", "config.dev.yaml")
	}

	cfg, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("carregando configuração de teste (%s): %w", path, err)
	}
	return cfg, nil
}

func checkIMAPReachable(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, imapDialTimeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

// populateMailbox roda docker/populate-mailbox/populate_mailbox.py
// pra garantir que a caixa de teste está com os fixtures de
// docker/emails antes de qualquer teste rodar.
func populateMailbox(repoRoot string) error {
	emailsDir := filepath.Join(repoRoot, "docker", "emails")

	hasFixtures, err := hasEmlFiles(emailsDir)
	if err != nil {
		return fmt.Errorf("verificando %s: %w", emailsDir, err)
	}
	if !hasFixtures {
		return fmt.Errorf(
			"nenhum arquivo .eml encontrado em %s — popule essa pasta antes de rodar os testes de integração "+
				"(ver docker/generate-emails/README.md e docker/populate-mailbox/README.md)",
			emailsDir,
		)
	}

	scriptPath := filepath.Join(repoRoot, "docker", "populate-mailbox", "populate_mailbox.py")
	cmd := exec.Command("python3", scriptPath)
	cmd.Dir = filepath.Dir(scriptPath)

	output, err := cmd.CombinedOutput()
	fmt.Print(string(output))
	if err != nil {
		return fmt.Errorf("falha ao rodar %s: %w", scriptPath, err)
	}

	return nil
}

func hasEmlFiles(dir string) (bool, error) {
	found := false
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".eml") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return found, nil
}
