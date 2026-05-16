// Exemplo mínimo de uso da biblioteca loghub_ident.
//
// Como rodar:
//
// Variáveis mínimas necessárias:
//
//	DATADIR    — diretório existente (default: /data)
//	WORKSPACE  — obrigatório, sem fallback (ex.: "production")
//
// Estrutura de /data esperada:
//
//	/data/
//	├── workspace      (arquivo com nome do workspace, ex.: "production\n")
//	├── agent_name     (opcional; nome do agente)
//	└── agent_uuid     (opcional; UUID persistente; gerado automaticamente se ausente)
//
// Exemplo de execução:
//
//	mkdir -p /tmp/mydata
//	echo "staging" > /tmp/mydata/workspace
//	DATADIR=/tmp/mydata MACHINE_ID=abcdef0123456789abcdef0123456789 \
//	  WORKSPACE=staging go run .
//
// Ou com todas as variáveis via env:
//
//	DATADIR=/tmp/mydata \
//	MACHINE_ID=abcdef0123456789abcdef0123456789 \
//	AGENT_NAME=my-service \
//	AGENT_UUID=a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d \
//	HOSTNAME=node01 \
//	WORKSPACE=production \
//	go run .
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
