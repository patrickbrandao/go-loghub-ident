package main

import (
	"fmt"
	"sync"
	"time"

	lhident "github.com/patrickbrandao/go-loghub-ident"
)

func main() {
	// 1. Inicialização obrigatória no início do main, antes de goroutines
	lhident.Initialize()

	fmt.Println("=== Loghub Ident: Serviço Inicializado com Sucesso ===")
	fmt.Printf("DataDir:   %s\n", lhident.DataDir())
	fmt.Printf("MachineID: %s\n", lhident.MachineID())
	fmt.Printf("AgentName: %s\n", lhident.AgentName())
	fmt.Printf("AgentUUID: %s\n", lhident.AgentUUID())
	fmt.Printf("Hostname:  %s\n", lhident.Hostname())
	fmt.Printf("Workspace: %s\n", lhident.Workspace())
	fmt.Println("======================================================")

	// 2. Demonstração de leitura concorrente segura (zero-lock happens-before)
	var wg sync.WaitGroup
	for i := 1; i <= 4; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			// Getters são O(1) e podem ser chamados milhares de vezes sem locks
			agent := lhident.AgentName()
			uuid := lhident.AgentUUID()
			ws := lhident.Workspace()
			fmt.Printf("[Worker %d] Ativo no workspace %q | Agente: %s (%s)\n", workerID, ws, agent, uuid)
		}(i)
	}
	wg.Wait()

	// 3. Simulação de ciclo de vida da aplicação
	time.Sleep(100 * time.Millisecond)
	fmt.Println("Execução concluída com sucesso.")
}
