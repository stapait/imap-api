// Package config carrega as configurações do servidor a partir de um
// arquivo YAML. As credenciais IMAP não entram aqui — chegam por
// requisição, via headers (ver internal/httpapi).
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	IMAP   IMAPConfig   `yaml:"imap"`
	Search SearchConfig `yaml:"search"`
}

type ServerConfig struct {
	Addr string `yaml:"addr"`
}

type IMAPConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	TLS  bool   `yaml:"tls"`
}

// SearchConfig configura GET /emails/search.
type SearchConfig struct {
	// FolderConcurrency é o número máximo de pastas buscadas em paralelo
	// (uma goroutine + conexão IMAP própria por pasta) quando a busca é
	// em todas as pastas (folder ausente na query). Deliberadamente sem
	// valor default embutido no código — precisa vir do arquivo de
	// configuração (validado em Load).
	FolderConcurrency int `yaml:"folderConcurrency"`
}

// Addr retorna o endereço host:port do servidor IMAP configurado.
func (c IMAPConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// Load lê e faz o parse do arquivo de configurações no caminho informado.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lendo arquivo de configuração %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parseando arquivo de configuração %q: %w", path, err)
	}

	if cfg.Search.FolderConcurrency <= 0 {
		return nil, fmt.Errorf("configuração %q: search.folderConcurrency precisa ser um inteiro > 0", path)
	}

	return &cfg, nil
}
