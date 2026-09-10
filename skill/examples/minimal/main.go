// Exemplo mínimo de uso da biblioteca loghubident.
//
// Como rodar (caminho feliz, tudo via env, sem tocar o disco):
//
//	MACHINE_ID=abcdef0123456789abcdef0123456789 \
//	AGENT_NAME=my-service \
//	AGENT_UUID=019e99e3-42f0-7882-9719-2305ff84949c \
//	HOSTNAME=node01 \
//	WORKSPACE=production \
//	go run .
//
// Como rodar gerando/persistindo identidade em um volume:
//
//	mkdir -p /tmp/mydata
//	DATADIR=/tmp/mydata WORKSPACE=staging go run .
//	# machine_id e agent_uuid são gerados e gravados em /tmp/mydata
//
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
