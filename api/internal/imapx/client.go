// Package imapx concentra o código de conexão/autenticação com o
// servidor IMAP, reaproveitado por todos os handlers HTTP que precisam
// falar com o IMAP.
package imapx

import (
	"errors"
	"fmt"
	"mime"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"

	"imap-api/internal/config"
)

// ErrAuthFailed indica que a conexão com o servidor IMAP foi
// estabelecida, mas o login com as credenciais informadas falhou.
var ErrAuthFailed = errors.New("autenticação IMAP falhou")

// clientOptions habilita a decodificação de RFC 2047 encoded-words
// (usados em Subject/From etc.) num conjunto amplo de charsets, via
// go-message/charset — sem isso, o go-imap só decodifica us-ascii,
// iso-8859-1 e utf-8 (mime.WordDecoder puro da stdlib), deixando
// qualquer outro charset (ex: ISO-2022-JP) sem decodificar.
var clientOptions = &imapclient.Options{
	WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
}

// dial abre a conexão TCP com o servidor IMAP configurado, com ou sem
// TLS conforme cfg.TLS.
func dial(cfg config.IMAPConfig) (*imapclient.Client, error) {
	if cfg.TLS {
		return imapclient.DialTLS(cfg.Addr(), clientOptions)
	}
	return imapclient.DialInsecure(cfg.Addr(), clientOptions)
}

// WithClient conecta ao servidor IMAP configurado, autentica com as
// credenciais informadas, executa fn e sempre encerra a conexão (logout
// + close) ao final, mesmo se fn retornar erro.
//
// Erro de autenticação é reportado como ErrAuthFailed (use errors.Is
// pra distinguir de erro de conexão/protocolo).
func WithClient(cfg config.IMAPConfig, user, password string, fn func(*imapclient.Client) error) error {
	client, err := dial(cfg)
	if err != nil {
		return fmt.Errorf("conectando ao servidor IMAP %s: %w", cfg.Addr(), err)
	}
	defer func() {
		_ = client.Logout().Wait()
		_ = client.Close()
	}()

	if err := client.Login(user, password).Wait(); err != nil {
		return fmt.Errorf("%w: %v", ErrAuthFailed, err)
	}

	return fn(client)
}
