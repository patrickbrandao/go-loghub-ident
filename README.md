# lib-loghub-ident

Biblioteca Go que **resolve e expõe a identidade de um software** dentro do
ecossistema Loghub. Preenche seis campos — `DataDir`, `MachineID`, `AgentName`,
`AgentUUID`, `Hostname`, `Workspace` — a partir de variáveis de ambiente,
arquivos e fallbacks do sistema. Projetada para microserviços em containers.

- **Leve e rápida:** após `Initialize`, cada getter é apenas a leitura de uma
  variável de pacote — sem locks, adequado a centenas de threads chamando
  milhares de vezes por milissegundo.
- **Imutável após `Initialize`:** os valores são escritos uma única vez no boot
  e nunca mais modificados.
- **Multiplataforma:** UUIDv7 gerado localmente via
  [`go-loghub-uuid`](https://github.com/patrickbrandao/go-loghub-uuid), sem
  dependência de `/proc`.

> A especificação completa de design está em [SPEC.md](SPEC.md).

## Instalação

```bash
go get github.com/patrickbrandao/go-loghub-ident
```

Requer **Go 1.22+**.

## Uso


Arquivo: go.mod
```go
module ident-test

go 1.22

require github.com/patrickbrandao/go-loghub-uuid  v0.1.0
require github.com/patrickbrandao/go-loghub-ident v0.2.0
```

Arquivo: main.go
```go
// Defina LOGHUB_IDENT_DEBUG=1 para ver a origem de cada valor em stderr.
package main

import (
    "fmt"

    lhident "github.com/patrickbrandao/go-loghub-ident"
)

func main() {
    lhident.Initialize() // encerra o processo se algo falhar

    fmt.Printf("DataDir:   %s\n", lhident.DataDir())
    fmt.Printf("MachineID: %s\n", lhident.MachineID())
    fmt.Printf("AgentName: %s\n", lhident.AgentName())
    fmt.Printf("AgentUUID: %s\n", lhident.AgentUUID())
    fmt.Printf("Hostname:  %s\n", lhident.Hostname())
    fmt.Printf("Workspace: %s\n", lhident.Workspace())
}
```

Compilar:
```bash
#go mod download;
GOSUMDB=off go mod download;
#go env -w GOPROXY=direct && go mod download;
go get ident-test;
#go build .;
go build -o ident-uuid main.go;
```

Compilar (alternativa):
```bash
go env -w GOSUMDB=off;
go get github.com/patrickbrandao/go-loghub-ident;
go mod tidy;
go build -o ident-test main.go;
```

Compilar (multi plataforma):
```
# Windows
GOOS=windows GOARCH=amd64 go build -o ident-test.exe main.go

# macOS
GOOS=darwin GOARCH=amd64 go build -o ident-test main.go

# Linux (outros processadores)
GOOS=linux GOARCH=arm64 go build -o ident-test main.go
```

Rodar:
```bash
./ident-test;
    # DataDir:   /data
    # MachineID: 79fb72c3199f4b85978420223a389b94
    # AgentName: ident-test
    # AgentUUID: 019e9e7e-bc45-76b6-8351-4430b52c4070
    # Hostname:  debv.tmsoft.com.br
    # Workspace: default
```




### Regras de uso

- Chame `Initialize()` **uma única vez**, no início de `main()`, **antes** de
  criar qualquer goroutine que vá ler os getters. Esse contrato é o que torna a
  leitura concorrente segura sem sincronização.
- Uma segunda chamada a `Initialize()` encerra o processo com **código 112**.

## API

| Função          | Retorno  | Descrição                                  |
|-----------------|----------|--------------------------------------------|
| `Initialize()`  | —        | resolve a identidade; encerra em falha     |
| `DataDir()`     | `string` | diretório de dados                         |
| `MachineID()`   | `string` | id da máquina (32 hex, sem hífen)          |
| `AgentName()`   | `string` | nome do agente                             |
| `AgentUUID()`   | `string` | UUIDv7 canônico (com hífens)               |
| `Hostname()`    | `string` | hostname                                   |
| `Workspace()`   | `string` | workspace/tenant                           |

## Resolução de cada campo

Precedência geral: **variável de ambiente → arquivo de disco → fallback**.
Para cada fonte: ler 1ª linha → trim → lowercase → sanear → validar regex.
Uma fonte **ausente** cai para a próxima; uma env **presente mas inválida**
encerra o processo.

| Campo        | Env             | Fontes de fallback                                              | Regex             |
|--------------|-----------------|----------------------------------------------------------------|-------------------|
| `DATADIR`    | `DATADIR`       | `/data` (validado de forma preguiçosa)                         | caminho de dir    |
| `MACHINE_ID` | `MACHINE_ID`    | `$MACHINE_ID_FILE` (def. `/etc/machine-id`) → `$DATADIR/machine_id` → gerado | `^[0-9a-f]{32}$` |
| `AGENT_NAME` | `AGENT_NAME`    | `$DATADIR/agent_name` → `argv[0]` (base, sem `.exe`)           | `^[a-z0-9._-]+$`  |
| `AGENT_UUID` | `AGENT_UUID`    | `$DATADIR/agent_uuid` → gerado (UUIDv7)                        | UUIDv7 canônico   |
| `HOSTNAME`   | `HOSTNAME`      | `os.Hostname()`                                                | `^[a-z0-9.-]+$`   |
| `WORKSPACE`  | `WORKSPACE`     | `$DATADIR/workspace` → `default`                              | `^[a-z0-9.-]+$`   |

### Persistência

- Apenas identidades **geradas aleatoriamente** (`machine_id`, `agent_uuid`) são
  gravadas em `$DATADIR` (permissão `0644`), para que o mesmo valor seja
  reobtido em execuções futuras. Valores determinísticos (`argv[0]`, `default`)
  nunca gravam arquivo.
- A biblioteca **não cria** o `DATADIR`; o container/orquestrador deve montar o
  volume. No caminho feliz (todas as envs presentes), `$DATADIR` nunca é tocado —
  compatível com filesystem read-only.

## Diagnóstico

Defina a env `LOGHUB_IDENT_DEBUG` (qualquer valor não vazio) para que
`Initialize()` escreva em stderr uma linha por campo indicando a origem do valor
(`env` / `file` / `generated` / `fallback`).

## Códigos de saída

Em qualquer falha, escreve `lib-loghub-ident: <VARIÁVEL>: <motivo>` em stderr e
chama `os.Exit(<código>)`.

| Código | Variável     | Motivo                                                 |
|--------|--------------|--------------------------------------------------------|
| 100    | `DATADIR`    | diretório necessário não existe ou não é diretório     |
| 102    | `MACHINE_ID` | env não casa com `^[0-9a-f]{32}$`                       |
| 103    | `AGENT_NAME` | todas as fontes vazias (`argv[0]` ficou vazio)        |
| 104    | `AGENT_NAME` | valor não casa com `^[a-z0-9._-]+$`                     |
| 105    | `AGENT_UUID` | falha na geração local de UUIDv7                      |
| 106    | `AGENT_UUID` | gravação em `$DATADIR/agent_uuid` falhou              |
| 107    | `AGENT_UUID` | valor não casa com a regex de UUIDv7                  |
| 108    | `HOSTNAME`   | env vazia e `os.Hostname()` retornou erro             |
| 109    | `HOSTNAME`   | valor não casa com `^[a-z0-9.-]+$`                     |
| 111    | `WORKSPACE`  | valor não casa com `^[a-z0-9.-]+$`                     |
| 112    | (geral)      | `Initialize()` chamado mais de uma vez                |
| 113    | `MACHINE_ID` | gravação em `$DATADIR/machine_id` falhou              |

> Em relação ao rascunho original: removidos os códigos **101** (machine-id é
> sempre regenerável) e **110** (workspace tem fallback `default`); adicionado o
> **113** (falha de gravação do machine_id).

## Exemplo

Veja [`examples/minimal`](examples/minimal) para um programa executável.

## Licença

Ver [LICENSE](LICENSE).
