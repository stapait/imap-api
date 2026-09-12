package httpapi

import "net/http"

// healthResponse é o body de GET /health.
type healthResponse struct {
	Status string `json:"status"`
}

// handleHealth implementa GET /health: confirma só que o processo da
// API está no ar e respondendo — não verifica o servidor IMAP nem
// nenhuma outra dependência externa (diferente do "conta" que os
// demais endpoints exigem, este não recebe/precisa de credenciais
// IMAP, já que não fala com nenhuma conta específica).
//
// @Summary Health check
// @Description Confirma que o processo da API está no ar e respondendo — não verifica conectividade com o servidor IMAP configurado nem nenhuma outra dependência externa.
// @Tags operacional
// @Produce json
// @Success 200 {object} healthResponse
// @Router /health [get]
func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}
