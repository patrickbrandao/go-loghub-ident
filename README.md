# lib-loghub-ident

Biblioteca Go que resolve e expõe a identidade de um software dentro do ecossistema Loghub. Preenche seis campos (DataDir, MachineID, AgentName, AgentUUID, Hostname, Workspace) a partir de variáveis de ambiente, arquivos e fallbacks do sistema. Projetada para microserviços em containers. UUID v4 gerado via `crypto/rand`. Arquivo de machine-id configurável via env `MACHINE_ID_FILE`.

## Instalação

```bash
go get github.com/patrickbrandao/go-loghub-ident
```

## Uso

```go
package main

import (
    "fmt"
    "github.com/patrickbrandao/go-loghub-ident"
)

func main() {
    loghub_ident.Initialize() // encerra o processo se algo falhar
    fmt.Println("agent:", loghub_ident.ID.AgentName, loghub_ident.ID.AgentUUID)
}
```

## Variáveis de identidade

Precedência: **(1) variável de ambiente → (2) arquivo de disco → (3) fallback do sistema**.

Cada fonte: ler primeira linha → trim whitespace → forçar lowercase → sanear → validar regex.

| Variável     | Env           | Arquivo de fallback             | Fallback final                                                            | Regex final                       | Códigos       |
|--------------|---------------|---------------------------------|---------------------------------------------------------------------------|-----------------------------------|---------------|
| `DATADIR`    | `DATADIR`     | —                               | `/data`                                                                   | (diretório existente)             | 100           |
| `MACHINE_ID` | `MACHINE_ID`  | `$MACHINE_ID_FILE` (padrão `/etc/machine-id`) | —                                                             | `^[0-9a-f]{32}$`                  | 101, 102      |
| `AGENT_NAME` | `AGENT_NAME`  | `$DATADIR/agent_name`           | `filepath.Base(os.Args[0])` saneado                                       | `^[a-z0-9._-]+$`                  | 103, 104      |
| `AGENT_UUID` | `AGENT_UUID`  | `$DATADIR/agent_uuid`           | gerar via `crypto/rand` e gravar em `$DATADIR/agent_uuid`                 | UUID v4 canônico                  | 105, 106, 107 |
| `HOSTNAME`   | `HOSTNAME`    | —                               | `os.Hostname()`                                                            | `^[a-z0-9.-]+$`                   | 108, 109      |
| `WORKSPACE`  | `WORKSPACE`   | `$DATADIR/workspace`            | — (obrigatório; sem fallback)                                              | `^[a-z0-9.-]+$`                   | 110, 111      |

## Códigos de saída

Em qualquer falha, a biblioteca escreve em stderr:
```
lib-loghub-ident: <VARIÁVEL>: <motivo>
```
e chama `os.Exit(<código>)`.

| Código | Variável     | Motivo                                                                                  |
|--------|--------------|-----------------------------------------------------------------------------------------|
| 100    | `DATADIR`    | diretório informado (env ou `/data`) não existe ou não é um diretório                   |
| 101    | `MACHINE_ID` | env vazio e arquivo de machine-id (`$MACHINE_ID_FILE`) ausente ou ilegível               |
| 102    | `MACHINE_ID` | valor obtido não casa com `^[0-9a-f]{32}$` após remover `-` e aplicar lowercase         |
| 103    | `AGENT_NAME` | env vazio, arquivo `$DATADIR/agent_name` ausente/vazio e `argv[0]` saneado ficou vazio  |
| 104    | `AGENT_NAME` | valor obtido não casa com `^[a-z0-9._-]+$`                                              |
| 105    | `AGENT_UUID` | falha na geração local de UUID v4 via `crypto/rand`                                     |
| 106    | `AGENT_UUID` | gravação em `$DATADIR/agent_uuid` (perm 0644) falhou                                    |
| 107    | `AGENT_UUID` | valor obtido não casa com regex de UUID v4 canônico                                     |
| 108    | `HOSTNAME`   | env vazio e `os.Hostname()` retornou erro                                               |
| 109    | `HOSTNAME`   | valor obtido não casa com `^[a-z0-9.-]+$` após lowercase                                |
| 110    | `WORKSPACE`  | env vazio e `$DATADIR/workspace` ausente ou vazio                                       |
| 111    | `WORKSPACE`  | valor obtido não casa com `^[a-z0-9.-]+$`                                               |
| 112    | (geral)      | `Initialize()` chamado mais de uma vez                                                   |

## Como rodar em container

### Dockerfile

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /myservice ./examples/minimal/

FROM alpine:3.19
COPY --from=builder /myservice /usr/local/bin/myservice
ENTRYPOINT ["myservice"]
```

### docker run

```bash
# Cria volume de dados com workspace
mkdir -p /tmp/agent-data
echo "production" > /tmp/agent-data/workspace

# Roda o container
docker run --rm \
  -v /tmp/agent-data:/data \
  -e WORKSPACE=production \
  -e MACHINE_ID=abcdef0123456789abcdef0123456789 \
  myservice:latest
```

## Como testar

```bash
# Testes unitários (todas as plataformas)
go test ./...

# Testes de integração
go test -tags=integration ./...

# Com verbose
go test -v ./...
go test -v -tags=integration ./...
```

## Garantias

- **Imutável após Initialize.** `ID` e seus campos nunca mudam após a chamada a `Initialize()`. Goroutines leem `ID.*` direto, sem sincronização.
- **Sem dependências externas.** Apenas a biblioteca padrão do Go. UUID v4 gerado via `crypto/rand`.
- **Multiplataforma.** UUID gerado localmente. Arquivo de machine-id configurável via env `MACHINE_ID_FILE` (padrão `/etc/machine-id`).
- **Go 1.21+.** Versão mínima exigida.
- **Chamada única.** `Initialize()` só pode ser chamada uma vez; segunda chamada encerra com código 112.
- **DATADIR preexistente.** A biblioteca não cria o diretório de dados; o container/orquestrador deve montar o volume.
