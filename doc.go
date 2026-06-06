// Package loghubident resolve e expõe a identidade de um software dentro do
// ecossistema Loghub. É carregado uma única vez no boot do processo
// ([Initialize]) e disponibiliza seis valores imutáveis por meio de getters de
// pacote.
//
// Os seis campos resolvidos são:
//
//   - DataDir   — diretório de dados persistente (env DATADIR ou /data)
//   - MachineID — identificador da máquina (env MACHINE_ID, arquivo
//     configurável via MACHINE_ID_FILE, $DATADIR/machine_id, ou gerado)
//   - AgentName — nome do agente (env AGENT_NAME, $DATADIR/agent_name ou argv[0])
//   - AgentUUID — UUIDv7 persistente (env AGENT_UUID, $DATADIR/agent_uuid ou gerado)
//   - Hostname  — hostname (env HOSTNAME ou os.Hostname)
//   - Workspace — workspace/tenant (env WORKSPACE, $DATADIR/workspace ou "default")
//
// Precedência geral de cada campo: variável de ambiente → arquivo de disco →
// fallback do sistema. Uma fonte ausente cai para a próxima; uma env presente
// mas inválida encerra o processo com o código do campo. Todos os valores são
// normalizados para lowercase e validados por regex.
//
// O UUIDv7 é gerado localmente via github.com/patrickbrandao/go-loghub-uuid,
// sem dependência de /proc, sendo portanto multiplataforma.
//
// # Uso rápido
//
//	package main
//
//	import (
//	    "fmt"
//
//	    lhident "github.com/patrickbrandao/go-loghub-ident"
//	)
//
//	func main() {
//	    lhident.Initialize() // encerra o processo se algo falhar
//	    fmt.Println("agent:", lhident.AgentName(), lhident.AgentUUID())
//	}
//
// Defina a variável de ambiente LOGHUB_IDENT_DEBUG para que Initialize escreva
// em stderr a origem de cada valor resolvido.
package loghubident
