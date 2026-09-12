// Command server sobe a API HTTP que expõe operações IMAP.
package main

import (
	"log"
	"net/http"
	"os"

	"imap-api/internal/config"
	"imap-api/internal/httpapi"

	// registra o spec gerado por `go generate ./...` (ver docs.go) no
	// pacote swag, pra httpSwagger.Handler (internal/httpapi/server.go)
	// conseguir servi-lo em /swagger.
	_ "imap-api/docs"
)

// @title imap-api
// @version 0.1
// @description API HTTP que abstrai operações IMAP. Multi-conta: cada requisição carrega as credenciais IMAP da conta a ser acessada, nos headers imapUser/imapPassword (ver descrição de cada endpoint) — a API não guarda usuário nenhum, só repassa pro servidor IMAP configurado.
// @BasePath /

// go:generate roda com o cwd igual ao diretório deste arquivo
// (cmd/server/) — o "cd ../.." volta pra raiz do módulo (api/) antes
// de chamar o swag, que é onde -g/-o abaixo fazem sentido (mesmo
// diretório de onde `task swagger:generate`/`go generate ./...` são
// chamados normalmente).
//go:generate sh -c "cd ../.. && go tool swag init -g cmd/server/main.go -o docs --parseInternal"

func main() {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config/config.dev.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("carregando configuração: %v", err)
	}

	handler := httpapi.NewServer(cfg)

	log.Printf("servidor escutando em %s (backend IMAP: %s)", cfg.Server.Addr, cfg.IMAP.Addr())
	if err := http.ListenAndServe(cfg.Server.Addr, handler); err != nil {
		log.Fatal(err)
	}
}
