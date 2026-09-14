package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Os exemplos de skill/examples são programas completos que consomem a
// biblioteca pelo caminho de módulo (com replace para este checkout). Compilar
// e executar cada um garante que a API pública, o go.mod dos exemplos e a
// dependência de UUID continuam coerentes com o código atual — é o mesmo
// caminho que um projeto consumidor percorre.

// examples lista os exemplos que precisam compilar e rodar.
var examples = []string{"minimal", "basic"}

// buildExample compila o exemplo com o go do PATH e devolve o binário.
func buildExample(t *testing.T, name string) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("binário go ausente no PATH")
	}
	dir := filepath.Join("..", "skill", "examples", name)
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("exemplo %s sem go.mod: %v", name, err)
	}
	bin := filepath.Join(t.TempDir(), name)
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command(goBin, "build", "-o", bin, ".")
	cmd.Dir = dir
	cmd.Env = os.Environ() // o build precisa de GOPATH/GOCACHE/HOME da máquina
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build do exemplo %s: %v\n%s", name, err, out)
	}
	return bin
}

// runExample executa o binário com um ambiente LIMPO acrescido de env, como o
// harness faz com o helper.
func runExample(t *testing.T, bin string, env map[string]string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = flatten(env)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	code = exitCode(t, cmd.Run())
	return code, out.String(), errOut.String()
}

// Caminho feliz: tudo via env, sem tocar o disco, e cada exemplo imprime os
// seis valores resolvidos sem escrever nada em stderr.
func TestExamples_HappyPathFromEnv(t *testing.T) {
	for _, name := range examples {
		t.Run(name, func(t *testing.T) {
			bin := buildExample(t, name)
			env := fullEnv()
			code, stdout, stderr := runExample(t, bin, env)
			if code != 0 {
				t.Fatalf("exit=%d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if stderr != "" {
				t.Errorf("caminho feliz deveria ser silencioso em stderr:\n%s", stderr)
			}
			for label, want := range map[string]string{
				"DataDir":   env["DATADIR"],
				"MachineID": env["MACHINE_ID"],
				"AgentName": env["AGENT_NAME"],
				"AgentUUID": env["AGENT_UUID"],
				"Hostname":  env["HOSTNAME"],
				"Workspace": env["WORKSPACE"],
			} {
				if !strings.Contains(stdout, label+":") || !strings.Contains(stdout, want) {
					t.Errorf("saída não traz %s = %q:\n%s", label, want, stdout)
				}
			}
		})
	}
}

// O exemplo mínimo gerando e persistindo identidade num volume: a primeira
// execução grava machine_id e agent_uuid; a segunda reutiliza os mesmos.
func TestExamples_MinimalPersistsGeneratedIdentity(t *testing.T) {
	bin := buildExample(t, "minimal")
	env := without(withDataDir(t), "MACHINE_ID", "AGENT_UUID")

	code, first, stderr := runExample(t, bin, env)
	if code != 0 {
		t.Fatalf("exit=%d\nstdout:\n%s\nstderr:\n%s", code, first, stderr)
	}
	uuid := readIdentFile(t, env["DATADIR"], "agent_uuid")
	mid := readIdentFile(t, env["DATADIR"], "machine_id")
	if !strings.Contains(first, "AgentUUID: "+uuid) || !strings.Contains(first, "MachineID: "+mid) {
		t.Errorf("a saída não reflete o que foi persistido (uuid=%s mid=%s):\n%s", uuid, mid, first)
	}

	code, second, stderr := runExample(t, bin, env)
	if code != 0 {
		t.Fatalf("segunda execução: exit=%d\nstderr:\n%s", code, stderr)
	}
	if second != first {
		t.Errorf("a identidade mudou entre execuções:\n--- primeira ---\n%s--- segunda ---\n%s", first, second)
	}
}

// Um exemplo também precisa falhar como a biblioteca promete: código de saída
// e mensagem padronizada em stderr, nada em stdout.
func TestExamples_MinimalFailsWithLibraryExitCode(t *testing.T) {
	bin := buildExample(t, "minimal")
	env := fullEnv()
	env["AGENT_UUID"] = "nao-e-um-uuid"
	code, stdout, stderr := runExample(t, bin, env)
	if code != 107 {
		t.Fatalf("exit=%d (esperava 107)\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.HasPrefix(stderr, "lib-loghub-ident: AGENT_UUID: ") {
		t.Errorf("stderr fora do formato padrão:\n%s", stderr)
	}
	if stdout != "" {
		t.Errorf("nada deveria ir para stdout na falha:\n%s", stdout)
	}
}
