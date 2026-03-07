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
# Auto-detecta seu editor (VSCode, Cursor, Windsurf, Cline, OpenCode, Antigravity)
# e gera/atualiza a configuração MCP apropriada. Use a flag -e para forçar um editor.
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
| `ciam_adr_search` | Busca em ADRs — entende o **porquê** de decisões arquiteturais |
| `ciam_prd_search` | Busca em PRDs — requisitos e critérios de aceite de uma feature |
| `ciam_plan_search` | Busca em planos de implementação |
| `ciam_research_search` | Busca em documentos de pesquisa (`docs/research/`) |
| `ciam_decision_context` | **Tudo em uma chamada**: código + ADR + PRD + plans para um query. Use antes de implementar qualquer coisa. |
| `ciam_draft` | **Opcional**: gera um rascunho de código via Ollama local, com contexto do projeto + ADRs + plano referenciado. Ative com `CIAM_CODE_MODEL`. |

---

## Knowledge management — ADR, PRD e planos

Além do código, projetos reais têm duas camadas de contexto que a IA precisa conhecer:

- **ADR** (Architecture Decision Record): *"por que usamos UUID como PK?"*, *"por que DRF e não FastAPI?"*
- **PRD** (Product Requirements Document): *"o que esse endpoint precisa fazer?"*, *"quais são os critérios de aceite?"*

Sem isso, a IA responde ao *como* mas não conhece o *porquê*. O `ciam` resolve isso com um diretório `docs/` versionado no projeto.

### Fluxo — feature nova

```bash
# 1. Documente o PORQUÊ antes de qualquer código
ciam prd new "Sistema de billing via Stripe"
# → cria docs/prd/PRD-001-sistema-de-billing-via-stripe.md
# Edite o arquivo: preencha problema, objetivo e critérios de aceite

# 2. Crie um plano de implementação por fases
ciam plan new "Integração Stripe" --prd PRD-001
# → cria docs/plans/Plan-001-integracao-stripe.md
# Edite: defina fases, critérios de sucesso e pontos de pausa

# 3. Implemente (o assistente já tem contexto completo via MCP)

# 4. Indexe os docs para o assistente consultar no futuro
ciam index . --include-docs
```

### Fluxo — bug relevante

```bash
# 1. Antes de tocar no código, documente a causa-raiz
ciam adr new "Fix: race condition no worker de emails"
# → cria docs/adr/ADR-001-fix-race-condition-no-worker-de-emails.md
# Edite: contexto, decisão e consequências

# 2. Faça o fix

# 3. Indexe para que o assistente não cometa o mesmo erro
ciam index . --include-docs
```

### Comandos ADR

```bash
ciam adr new "<título>"               # Cria ADR-NNN.md com template MADR
ciam adr list                          # Lista todos com status
ciam adr supersede 3 "<novo título>"  # Marca ADR-003 como superseded e cria substituto
```

### Comandos PRD

```bash
ciam prd new "<título>"   # Cria PRD-NNN.md com template estruturado
ciam prd list             # Lista todos com status
```

### Comandos Plan

```bash
ciam plan new "<título>" [--prd PRD-001]  # Cria Plan-NNN.md com fases e checkpoints
ciam plan list                             # Lista todos com status
```

### Estrutura `docs/` gerada

```
docs/
├── adr/        ← architectural decision records (obrigatório em bugs relevantes)
├── prd/        ← product requirements documents (obrigatório antes de features)
├── plans/      ← planos de implementação por fases
└── research/   ← pesquisas e docs externos ingeridos
```

Todos os arquivos são Markdown, versionados com git, e ficam pesquisáveis via `ciam_decision_context` no assistente MCP.

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
| `CIAM_CODE_MODEL` | _(vazio)_ | Modelo LLM para `ciam_draft` (ex: `qwen2.5-coder:1.5b`). Vazio = recurso desabilitado. |
| `CIAM_DB_PATH` | `~/.local/share/ciam/ciam.db` | Caminho do banco SQLite |
| `CIAM_PROJECT_PATH` | `.` | Projeto padrão (setado pelo VSCode via mcp.json) |

---

## Comandos CLI

```bash
ciam up              # Sobe Ollama + API (docker compose up -d)
ciam index [path]    # Indexa um projeto
ciam index . --include-docs  # Indexa código + docs/ (ADR, PRD, plans, research)
ciam search <query>  # Busca no projeto indexado
ciam remember <text> # Salva na memória persistente
ciam recall <query>  # Busca na memória
ciam status          # Mostra métricas (chunks, memórias, tokens economizados)
ciam mcp             # Inicia o servidor MCP em modo stdio (chamado pelo VSCode)
ciam init            # Auto-detecta o editor e gera a configuração MCP (suporta flag -e)

# Knowledge management
ciam adr new "<título>"                # Novo ADR (MADR template)
ciam adr list                           # Lista ADRs
ciam adr supersede <N> "<novo título>" # Supersede ADR-N
ciam prd new "<título>"                # Novo PRD
ciam prd list                           # Lista PRDs
ciam plan new "<título>" [--prd PRD-001]  # Novo plano
ciam plan list                          # Lista planos

# ciam_draft — rascunho especulativo via Ollama (opcional)
export CIAM_CODE_MODEL=qwen2.5-coder:1.5b
ciam draft "view de registro de usuário"
ciam draft "serializer de pedido" --plan Plan-001 --phase "Fase 2"
ciam draft "celery task de envio de email" --type task --max-tokens 1024
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
│   │   └── commands/       # Subcomandos cobra (index, search, adr, prd, plan...)
│   └── server/             # Entry point da REST API
├── internal/
│   ├── api/                # REST API + cliente HTTP
│   ├── cache/              # Cache L1 (in-memory) + L2 (SQLite)
│   ├── config/             # Configuração via env vars
│   ├── docs/               # Gerenciamento de ADR/PRD/plans + templates embed
│   ├── embeddings/         # Cliente Ollama (embed + batch paralelo + generate para ciam_draft)
│   ├── indexer/
│   │   ├── indexer.go      # Indexer genérico + detecção de tipo
│   │   ├── django/         # Indexer Django-aware
│   │   └── docs/           # Indexer de docs/ (chunk_type: adr|prd|plan|research)
│   ├── memory/             # Memória persistente entre sessões
│   ├── mcp/                # Servidor MCP stdio (12 ferramentas)
│   ├── search/             # Busca híbrida BM25 + RRF + compressão
│   └── storage/            # SQLite (chunks + memórias + métricas)
├── docker-compose.yml
├── Dockerfile
├── Makefile
└── .vscode/
    └── mcp.json
```

---

## Releases

Os binários são gerados automaticamente pelo GitHub Actions a cada tag `v*.*.*`.

Para publicar uma nova versão:

```bash
git tag v1.0.0
git push origin v1.0.0
```

O pipeline constrói `ciam` para:

| Plataforma | Arch |
|---|---|
| Linux | amd64 |
| Linux | arm64 |
| macOS | amd64 (Intel) |
| macOS | arm64 (Apple Silicon) |

Os binários e o `checksums.txt` ficam disponíveis na página de [Releases](../../releases) do repositório.

---

## Licença

MIT
