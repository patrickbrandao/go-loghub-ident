package loghubident

import (
	"fmt"
	"os"
	"sync/atomic"
)

// initialized garante que Initialize só execute a resolução uma vez.
// O CompareAndSwap detecta a segunda chamada mesmo sob concorrência.
var initialized atomic.Bool

// Initialize resolve os seis campos de identidade a partir de variáveis de
// ambiente, arquivos e fallbacks do sistema, e os disponibiliza pelos getters
// de pacote (DataDir, MachineID, AgentName, AgentUUID, Hostname, Workspace).
//
// Deve ser chamada UMA ÚNICA VEZ, no início de main(), ANTES de criar qualquer
// goroutine que vá ler os getters. Esse contrato estabelece a relação
// happens-before que torna a leitura posterior segura sem sincronização.
//
// Em qualquer falha de resolução, escreve em stderr no formato
//
//	lib-loghub-ident: <VARIÁVEL>: <motivo>
//
// e encerra o processo com o código correspondente (ver SPEC.md / README).
// A segunda chamada encerra com código 112.
//
// Se a variável de ambiente LOGHUB_IDENT_DEBUG estiver definida, escreve em
// stderr uma linha por campo indicando a origem do valor resolvido.
func Initialize() {
	if !initialized.CompareAndSwap(false, true) {
		fmt.Fprintln(os.Stderr, "lib-loghub-ident: init: Initialize() chamado mais de uma vez")
		os.Exit(112)
	}

	sys := osSystem{}
	id, f := resolve(sys)
	if f != nil {
		fmt.Fprintf(os.Stderr, "lib-loghub-ident: %s: %s\n", f.variable, f.reason)
		os.Exit(f.code)
	}

	apply(id)

	if sys.Getenv(EnvDebug) != "" {
		for _, line := range id.debug {
			fmt.Fprintln(os.Stderr, "lib-loghub-ident: debug: "+line)
		}
	}
}
