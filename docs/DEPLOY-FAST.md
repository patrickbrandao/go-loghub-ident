# DEPLOY-FAST — Ambiente mínimo em 2 minutos

Mini tutorial direto ao ponto para criar um ambiente do zero, escrever um
programa que usa a biblioteca `go-loghub-ident` e compilá-lo.

## 1. Crie o diretório e o `go.mod`

```sh
mkdir test-ident && cd test-ident
go mod init test-ident
```

## 2. Adicione a dependência

```sh
go get github.com/patrickbrandao/go-loghub-ident
```

Isso preenche o `go.mod` e cria o `go.sum`. O `go.mod` deve ficar assim
(a versão exige Go 1.22+):

```
module test-ident

go 1.22

require github.com/patrickbrandao/go-loghub-ident v0.1.0
```

## 3. Escreva o `test-ident.go`

```go
package main

import (
	"fmt"

	lhident "github.com/patrickbrandao/go-loghub-ident"
)

func main() {
	lhident.Initialize() // resolve a identidade; encerra o processo em falha

	fmt.Printf("DataDir:   %s\n", lhident.DataDir())
	fmt.Printf("MachineID: %s\n", lhident.MachineID())
	fmt.Printf("AgentName: %s\n", lhident.AgentName())
	fmt.Printf("AgentUUID: %s\n", lhident.AgentUUID())
	fmt.Printf("Hostname:  %s\n", lhident.Hostname())
	fmt.Printf("Workspace: %s\n", lhident.Workspace())
}
```

## 4. Compile

```sh
go mod tidy        # garante go.sum completo
go build -o test-ident .
```

## 5. Execute (caminho feliz, tudo via env, sem tocar o disco)

```sh
MACHINE_ID=abcdef0123456789abcdef0123456789 \
AGENT_NAME=my-service \
AGENT_UUID=019e99e3-42f0-7882-9719-2305ff84949c \
HOSTNAME=node01 \
WORKSPACE=production \
./test-ident
```

## 6. Execute gerando e persistindo a identidade num volume

```sh
mkdir -p /tmp/mydata
DATADIR=/tmp/mydata WORKSPACE=staging ./test-ident
# machine_id e agent_uuid são gerados e gravados em /tmp/mydata
```

> Dica: defina `LOGHUB_IDENT_DEBUG=1` para ver, em stderr, a origem de cada
> valor resolvido.

Pronto. Para todos os detalhes de variáveis, fallbacks e códigos de saída,
veja [DEPLOY-FULL.md](DEPLOY-FULL.md).
