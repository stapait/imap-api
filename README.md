# Projeto IMAP API
Esta API disponibiliza endpoints HTTP que abstraem operações que utilizam o protocolo IMAP.

## O que a API faz

A API é desenvolvida em Go. Abaixo, uma visão geral dos endpoints (parte já implementada, parte ainda planejada) — a descrição técnica completa de cada um (parâmetros, formatos de request/response etc.) vai estar disponível via Swagger.

- `GET /folders` — lista as pastas de e-mail disponíveis na conta.
- `GET /folders/{pasta}` — informações sobre uma pasta específica (quantidade de mensagens, não lidas, etc.).
- `PUT /folders/{pasta}` — cria uma pasta.
- `PATCH /folders/{pasta}` — renomeia uma pasta.
- `DELETE /folders/{pasta}` — apaga uma pasta e suas subpastas.
- `GET /folders/{pasta}/emails` — lista os e-mails de uma pasta, de forma resumida (para exibição em uma lista/inbox).
- `DELETE /folders/{pasta}/emails` — esvazia uma pasta (apaga todas as mensagens).
- `GET /folders/{pasta}/emails/{uid}` — obtém um e-mail completo, com o corpo já processado (sem os anexos).
- `GET /folders/{pasta}/emails/{uid}/raw` — obtém o e-mail original, sem nenhum processamento.
- `PATCH /folders/{pasta}/emails/{uid}` — atualiza uma mensagem: marca como lida/não lida, favorita/desfavorita, marca como spam, ou move para outra pasta.
- `DELETE /folders/{pasta}/emails/{uid}` — apaga uma mensagem.
- `GET /folders/{pasta}/emails/{uid}/attachments` — lista os anexos de um e-mail.
- `GET /folders/{pasta}/emails/{uid}/attachments/{anexo}` — baixa um anexo específico.
- `GET /emails/search` — pesquisa e-mails em todas as pastas (ou numa específica, via filtro), por remetente/assunto/texto/data/etc.
- `GET /account` — informações da conta (status da caixa postal, cota de uso).

Mais operações serão adicionadas conforme o design avançar.

## Ambiente de desenvolvimento local

O projeto conta com uma automação Docker, na pasta `docker/`, que sobe um servidor Dovecot local para testes de IMAP durante o desenvolvimento — sem TLS, apenas para conexões locais. Veja `docker/docker-compose.yml` para detalhes de configuração.

## Documentação da API (Swagger)

O projeto possui uma documentação Swagger para todos os endpoints, gerada automaticamente e que pode ser utilizada para testes.

Com o Dovecot local executando e a API rodando localmente, a documentação do Swagger UI fica disponível em [`http://localhost:8080/swagger/`](http://localhost:8080/swagger/). É possível executar cada chamada do próprio browser.