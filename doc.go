// Package loghub_ident fornece a identidade do software dentro do ecossistema
// Loghub. A biblioteca é carregada uma única vez na inicialização do processo
// ([Initialize]) e expõe os seis valores de identidade em um singleton imutável
// ([ID]).
//
// Os seis campos resolvidos são:
//
//   - DataDir   — diretório de dados (env DATADIR ou /data)
//   - MachineID — identificador único da máquina (env MACHINE_ID ou arquivo
//     configurável via env MACHINE_ID_FILE, padrão /etc/machine-id)
//   - AgentName — nome do agente (env, arquivo ou argv[0])
//   - AgentUUID — UUID v4 persistente do agente (env, arquivo ou gerado
//     localmente via crypto/rand)
//   - Hostname  — hostname da máquina (env ou os.Hostname)
//   - Workspace — workspace lógico (env ou arquivo; obrigatório)
//
// Cada campo segue a precedência: variável de ambiente → arquivo de disco →
// fallback do sistema (quando disponível). Todos são normalizados para lowercase.
//
// Em caso de falha na resolução de qualquer campo, a biblioteca escreve uma
// mensagem de erro em stderr e encerra o processo via os.Exit com um código
// específico (100–112). Consulte a documentação de [Initialize] para detalhes.
//
// A biblioteca não possui dependências externas; utiliza apenas a biblioteca
// padrão do Go. Compatível com qualquer plataforma suportada por Go
// (UUID gerado via crypto/rand, sem dependência de /proc).
//
// # Uso rápido
//
//	package main
//
//	import (
//	    "fmt"
//	    "github.com/patrickbrandao/go-loghub-ident"
//	)
//
//	func main() {
//	    loghub_ident.Initialize()
//	    fmt.Println("Agent:", loghub_ident.ID.AgentName)
//	    fmt.Println("UUID:",  loghub_ident.ID.AgentUUID)
//	}
package loghub_ident

