# ciam — Context IA Manager

> Busca semântica e memória persistente para projetos Django (e qualquer projeto Python), integrado ao VSCode e Antigravity via protocolo MCP — reduzindo o uso de contexto de IA em até 98%.

Inspirado no [th0th](https://github.com/S1LV4/th0th), reescrito em **Go** com foco em projetos Django e sem dependência de runtime (binário único).

---

## Como funciona

Em vez de enviar arquivos inteiros para a IA, o `ciam` indexa seu projeto localmente com embeddings gerados pelo **Ollama** (100% offline) e expõe uma busca híbrida (semântica + keyword) para o assistente via **MCP**. O resultado é recuperação precisa de contexto com uma fração dos tokens.

```
VSCode / Antigravity
       │ MCP stdio
       ▼
  ciam mcp server  ──HTTP──▶  ciam API (:8080)  ──▶  Ollama (:11434)
                                    │
                                    ▼
                              SQLite (~/.local/share/ciam/ciam.db)
```

---

## Pré-requisitos

- [Go 1.22+](https://go.dev/dl/)
- [Docker](https://docs.docker.com/get-docker/) + [Docker Compose](https://docs.docker.com/compose/)
- VSCode com [Antigravity](https://marketplace.visualstudio.com/items?itemName=GitHub.copilot) ou qualquer cliente MCP

---

## Instalação

```bash
# Clone o repositório
git clone https://github.com/smkbarbosa/context-ia-manager
cd context-ia-manager

# Instala o binário ciam em ~/go/bin
make install

# Garanta que ~/go/bin está no PATH (adicione ao ~/.zshrc ou ~/.bashrc)
export PATH="$HOME/go/bin:$PATH"
```

---

## Quickstart

### 1. Suba os serviços

> Execute dentro da pasta do **context-ia-manager** (onde está o `docker-compose.yml`).

```bash
ciam up
# Sobe Ollama + ciam API via Docker Compose
# Faz o pull do modelo nomic-embed-text automaticamente
```

Os serviços ficam rodando em background. Você só precisa rodar `ciam up` uma vez por sessão (ou configurar para subir no boot).

### 2. Configure o workspace do seu projeto

> A partir daqui, execute dentro da pasta do **seu projeto Django**.

```bash
cd /seu/projeto-django
ciam init
# Gera .vscode/mcp.json — o VSCode/Antigravity reconhece automaticamente
```

### 3. Indexe o projeto

```bash
# Ainda dentro do seu projeto Django:
ciam index .
# Auto-detecta Django pela presença de manage.py

# Ou explicitamente:
ciam index . --type django
```

### 4. Busque

```bash
ciam search "autenticação JWT"
ciam search "model de pedido" --type model
ciam search "celery task de envio de email" --type task

# Com compressão (menos tokens na resposta):
ciam search "serializer de usuário" --compress
```

### 5. Memorize decisões importantes

```bash
ciam remember "decidimos usar UUID como PK em todos os models"
ciam remember "o campo email é único e indexado" --type decision

# Recupere depois (em qualquer projeto):
ciam recall "decisões sobre models"
```

> **Resumo de onde rodar cada coisa:**
>
> | Comando | Onde executar |
> |---|---|
> | `ciam up` / `ciam down` | pasta do `context-ia-manager` |
> | `ciam init` | pasta do seu projeto |
> | `ciam index` | pasta do seu projeto |
> | `ciam search` / `ciam remember` / `ciam recall` | qualquer lugar |
> | `ciam status` | qualquer lugar |

---

## Ferramentas MCP disponíveis

Quando o VSCode abre um workspace com `.vscode/mcp.json`, o assistente tem acesso automático a:

| Ferramenta | O que faz |
|---|---|
| `ciam_index` | Indexa o projeto para busca semântica |
| `ciam_search` | Busca híbrida (semântica + keyword) nos chunks indexados |
| `ciam_remember` | Armazena uma decisão/nota na memória persistente |
| `ciam_recall` | Recupera memórias de sessões anteriores |
| `ciam_compress` | Comprime código: mantém assinaturas, remove docstrings |
| `ciam_context` | Busca + comprime em uma chamada (máxima economia de tokens) |
| `ciam_django_map` | Retorna mapa estrutural do projeto: apps, models, views, urls |

---

## Django awareness

O indexador reconhece e categoriza automaticamente os arquivos pelo seu papel no projeto:

| Arquivo | Tipo atribuído |
|---|---|
| `models.py` | `model` |
| `views.py`, `viewsets.py` | `view` |
| `urls.py` | `url` |
| `serializers.py` | `serializer` |
| `tasks.py` | `task` |
| `signals.py` | `signal` |
| `managers.py` | `manager` |
| `admin.py` | `admin` |
| `test_*.py`, `*_test.py` | `test` |
| `settings.py` | `settings` |

Isso permite filtros precisos: `ciam search "autenticação" --type view` retorna apenas views, não models ou urls.

---

## Configuração via variáveis de ambiente

| Variável | Padrão | Descrição |
|---|---|---|
| `CIAM_API_URL` | `http://localhost:8080` | URL da ciam API |
| `CIAM_OLLAMA_URL` | `http://localhost:11434` | URL do Ollama |
| `CIAM_OLLAMA_MODEL` | `nomic-embed-text` | Modelo de embeddings |
| `CIAM_DB_PATH` | `~/.local/share/ciam/ciam.db` | Caminho do banco SQLite |
| `CIAM_PROJECT_PATH` | `.` | Projeto padrão (setado pelo VSCode via mcp.json) |

---

## Comandos CLI

```bash
ciam up              # Sobe Ollama + API (docker compose up -d)
ciam index [path]    # Indexa um projeto
ciam search <query>  # Busca no projeto indexado
ciam remember <text> # Salva na memória persistente
ciam recall <query>  # Busca na memória
ciam status          # Mostra métricas (chunks, memórias, tokens economizados)
ciam mcp             # Inicia o servidor MCP em modo stdio (chamado pelo VSCode)
ciam init            # Gera .vscode/mcp.json no diretório atual
```

```bash
# Makefile
make install         # go install ./cmd/ciam
make build           # compila em ./bin/ciam
make dev             # install + docker compose up
make up              # docker compose up -d
make down            # docker compose down
```

---

## Estrutura do projeto

```
context-ia-manager/
├── cmd/
│   ├── ciam/               # Entry point da CLI
│   │   └── commands/       # Subcomandos cobra
│   └── server/             # Entry point da REST API
├── internal/
│   ├── api/                # REST API + cliente HTTP
│   ├── config/             # Configuração via env vars
│   ├── embeddings/         # Cliente Ollama (embed + batch paralelo)
│   ├── indexer/
│   │   ├── indexer.go      # Indexer genérico + detecção de tipo
│   │   └── django/         # Indexer Django-aware
│   ├── memory/             # Memória persistente entre sessões
│   ├── mcp/                # Servidor MCP stdio
│   ├── search/             # Busca híbrida BM25 + RRF + compressão
│   └── storage/            # SQLite (chunks + memórias + métricas)
├── docker-compose.yml
├── Dockerfile
├── Makefile
└── .vscode/
    └── mcp.json
```

---

## Licença

MIT
