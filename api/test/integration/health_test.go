//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"
)

type healthResponse struct {
	Status string `json:"status"`
}

// TestHealth_Success cobre o caso feliz: 200, sem exigir headers
// imapUser/imapPassword — health check não fala com nenhuma conta IMAP
// específica, só confirma que o processo está no ar.
func TestHealth_Success(t *testing.T) {
	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("requisição falhou: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", resp.StatusCode, http.StatusOK)
	}

	var body healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decodificando resposta: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, esperado %q", body.Status, "ok")
	}
}
