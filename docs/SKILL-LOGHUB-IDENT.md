---
name: SKILL-LOGHUB-IDENT
description: Como incluir e usar a biblioteca Go loghub_ident para identificação de agentes em microserviços. Import path: github.com/patrickbrandao/go-loghub-ident
---

# Como usar a biblioteca loghub_ident

Biblioteca Go para identidade de agentes em microserviços. Resolve 6 campos de identidade (DataDir, MachineID, AgentName, AgentUUID, Hostname, Workspace) a partir de variáveis de ambiente, arquivos e fallbacks do sistema.

- **Sem dependências externas** — usa apenas stdlib do Go.
- **Go 1.21+** obrigatório.
- **Multiplataforma** — UUID gerado via `crypto/rand`; arquivo de machine-id configurável via env `MACHINE_ID_FILE` (padrão `/etc/machine-id`).

---

## 1. Instalar

```bash
go get github.com/patrickbrandao/go-loghub-ident
```

Isso adiciona a dependência ao `go.mod` do seu projeto.

---

## 2. Importar

```go
import "github.com/patrickbrandao/go-loghub-ident"
```

---

## 3. Inicializar

Chame `loghub_ident.Initialize()` **uma única vez**, no início do `main()`, **antes de criar goroutines** que leiam `loghub_ident.ID`.

```go
func main() {
    loghub_ident.Initialize()
    // a partir daqui, loghub_ident.ID está populado e é imutável
}
```

**Regras:**
- Se qualquer campo falhar na resolução, o processo encerra com `os.Exit(código)` e mensagem em stderr.
- Chamar `Initialize()` duas vezes encerra com código 112.
- Após `Initialize()`, `loghub_ident.ID` nunca muda. Leitura segura entre goroutines sem mutex.

---

## 4. Acessar os campos

Após `Initialize()`, acesse os campos pelo singleton `loghub_ident.ID`:

```go
loghub_ident.ID.DataDir   // string — diretório de dados, ex: "/data"
loghub_ident.ID.MachineID // string — 32 hex chars, ex: "abcdef0123456789abcdef0123456789"
loghub_ident.ID.AgentName // string — nome do agente, ex: "my-service"
loghub_ident.ID.AgentUUID // string — UUID v4, ex: "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"
loghub_ident.ID.Hostname  // string — hostname, ex: "node01"
loghub_ident.ID.Workspace // string — workspace, ex: "production"
```

Todos os valores são **lowercase**.

---

## 5. Exemplo completo

```go
package main

import (
    "fmt"
    "github.com/patrickbrandao/go-loghub-ident"
)

func main() {
    loghub_ident.Initialize()

    fmt.Printf("DataDir:   %s\n", loghub_ident.ID.DataDir)
    fmt.Printf("MachineID: %s\n", loghub_ident.ID.MachineID)
    fmt.Printf("AgentName: %s\n", loghub_ident.ID.AgentName)
    fmt.Printf("AgentUUID: %s\n", loghub_ident.ID.AgentUUID)
    fmt.Printf("Hostname:  %s\n", loghub_ident.ID.Hostname)
    fmt.Printf("Workspace: %s\n", loghub_ident.ID.Workspace)
}
```

---

## 6. Variáveis de ambiente e fallbacks

Cada campo segue a precedência: **env → arquivo → fallback**.

| Campo        | Env var         | Arquivo fallback                               | Fallback final               | Obrigatório |
|--------------|-----------------|------------------------------------------------|------------------------------|-------------|
| `DataDir`    | `DATADIR`       | —                                              | `/data`                      | Sim (dir deve existir) |
| `MachineID`  | `MACHINE_ID`    | `$MACHINE_ID_FILE` (padrão `/etc/machine-id`)  | —                            | Sim |
| `AgentName`  | `AGENT_NAME`    | `$DATADIR/agent_name`                          | nome do executável (argv[0]) | Sim |
| `AgentUUID`  | `AGENT_UUID`    | `$DATADIR/agent_uuid`                          | gera e grava automaticamente | Sim |
| `Hostname`   | `HOSTNAME`      | —                                              | `os.Hostname()`              | Sim |
| `Workspace`  | `WORKSPACE`     | `$DATADIR/workspace`                           | **nenhum** — sem fallback    | Sim |

**WORKSPACE não tem fallback.** Deve ser definido via env ou arquivo. Caso contrário, o processo encerra.

---

## 7. Executar

### Via variáveis de ambiente (mínimo)

```bash
DATADIR=/tmp/mydata \
MACHINE_ID=abcdef0123456789abcdef0123456789 \
WORKSPACE=production \
go run .
```

### Via arquivos em DATADIR

```bash
mkdir -p /tmp/mydata
echo "production" > /tmp/mydata/workspace
echo "my-service" > /tmp/mydata/agent_name

DATADIR=/tmp/mydata \
MACHINE_ID=abcdef0123456789abcdef0123456789 \
go run .
```

### Em container Docker

```bash
mkdir -p /tmp/agent-data
echo "production" > /tmp/agent-data/workspace

docker run --rm \
  -v /tmp/agent-data:/data \
  -e WORKSPACE=production \
  -e MACHINE_ID=abcdef0123456789abcdef0123456789 \
  myservice:latest
```

---

## 8. Códigos de saída em caso de erro

Se a inicialização falhar, a mensagem vai para stderr no formato:
```
lib-loghub-ident: <VARIÁVEL>: <motivo>
```

| Código | Campo        | Causa                                            |
|--------|--------------|--------------------------------------------------|
| 100    | DATADIR      | diretório não existe ou não é diretório           |
| 101    | MACHINE_ID   | env vazio e arquivo de machine-id ausente         |
| 102    | MACHINE_ID   | formato inválido (esperado: 32 hex lowercase)     |
| 103    | AGENT_NAME   | todas as fontes vazias                            |
| 104    | AGENT_NAME   | formato inválido                                  |
| 105    | AGENT_UUID   | falha na geração local de UUID v4                 |
| 106    | AGENT_UUID   | falha ao gravar $DATADIR/agent_uuid               |
| 107    | AGENT_UUID   | não é UUID v4 válido                              |
| 108    | HOSTNAME     | os.Hostname() falhou                              |
| 109    | HOSTNAME     | formato inválido                                  |
| 110    | WORKSPACE    | env vazio e arquivo ausente                       |
| 111    | WORKSPACE    | formato inválido                                  |
| 112    | (geral)      | Initialize() chamado mais de uma vez              |

---

## 9. Struct Identity (referência rápida)

```go
type Identity struct {
    DataDir   string // diretório de dados
    MachineID string // ^[0-9a-f]{32}$
    AgentName string // ^[a-z0-9._-]+$
    AgentUUID string // UUID v4 canônico
    Hostname  string // ^[a-z0-9.-]+$
    Workspace string // ^[a-z0-9.-]+$
}
```

---

## 10. Regras importantes

1. **Chamar `Initialize()` apenas uma vez.** Segunda chamada = exit 112.
2. **`ID` é imutável após `Initialize()`.** Leitura segura sem mutex.
3. **DATADIR deve existir antes.** A biblioteca não cria o diretório.
4. **AGENT_UUID é persistido.** Se gerado, é gravado em `$DATADIR/agent_uuid` (perm 0644).
5. **Falhas são fatais.** A biblioteca chama `os.Exit()`, não retorna `error`.
6. **Sem dependências externas.** Apenas stdlib. UUID v4 gerado via `crypto/rand`.
7. **MACHINE_ID_FILE.** Caminho do arquivo de machine-id configurável via env `MACHINE_ID_FILE` (padrão `/etc/machine-id`).
