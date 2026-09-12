// Package httpapi expõe os endpoints HTTP da API sobre net/http puro
// (stdlib), sem framework/router externo.
package httpapi

import (
	"encoding/json"
	"net/http"

	httpSwagger "github.com/swaggo/http-swagger/v2"

	"imap-api/internal/config"
)

// NewServer monta o roteador HTTP com todos os endpoints da API.
func NewServer(cfg *config.Config) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /account", handleGetAccount(cfg.IMAP))
	mux.HandleFunc("GET /folders", handleListFolders(cfg.IMAP))
	mux.HandleFunc("POST /folders/{pasta}", handleCreateFolder(cfg.IMAP))
	mux.HandleFunc("GET /folders/{pasta}", handleGetFolder(cfg.IMAP))
	mux.HandleFunc("PATCH /folders/{pasta}", handleRenameFolder(cfg.IMAP))
	mux.HandleFunc("DELETE /folders/{pasta}", handleDeleteFolder(cfg.IMAP))
	mux.HandleFunc("GET /folders/{pasta}/emails", handleListEmails(cfg.IMAP))
	mux.HandleFunc("PATCH /folders/{pasta}/emails/flags", handlePatchEmailFlags(cfg.IMAP))
	mux.HandleFunc("PATCH /folders/{pasta}/emails/move", handleMoveEmails(cfg.IMAP))
	mux.HandleFunc("DELETE /folders/{pasta}/emails", handleEmptyFolder(cfg.IMAP))
	mux.HandleFunc("GET /folders/{pasta}/emails/{uid}", handleGetEmail(cfg.IMAP))
	mux.HandleFunc("GET /folders/{pasta}/emails/{uid}/raw", handleGetRawEmail(cfg.IMAP))
	mux.HandleFunc("GET /folders/{pasta}/emails/{uid}/attachments/{attachmentId}", handleGetAttachment(cfg.IMAP))
	mux.HandleFunc("GET /emails/search", handleSearchEmails(cfg.IMAP, cfg.Search))

	// UI + spec (JSON/YAML) gerados a partir das anotações @... acima de
	// cada handler — ver "go generate ./..." (cmd/server/main.go) e o
	// pacote gerado imap-api/docs, importado via blank import em
	// cmd/server/main.go pra registrar o spec no pacote swag.
	mux.Handle("GET /swagger/", httpSwagger.Handler(httpSwagger.URL("/swagger/doc.json")))

	return mux
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}
