package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Esta suíte percorre a tabela de códigos de saída da SPEC §13 executando o
// binário de verdade. Três códigos não são alcançáveis por fora — 105 (falha na
// geração de UUID) e 108 (erro de os.Hostname) só ocorrem com o system injetado,
// e ambos já estão cobertos pelos testes unitários da raiz (resolve_test.go).

// checkFailure valida o código de saída e o formato da mensagem da SPEC §13:
//
//	lib-loghub-ident: <VARIÁVEL>: <motivo>
func checkFailure(t *testing.T, res result, code int, variable string) {
	t.Helper()
	if res.code != code {
		t.Fatalf("exit=%d (esperava %d)\nstdout:\n%s\nstderr:\n%s",
			res.code, code, res.stdout, res.stderr)
	}
	prefix := fmt.Sprintf("lib-loghub-ident: %s: ", variable)
	if !strings.HasPrefix(res.stderr, prefix) {
		t.Errorf("stderr não começa com %q:\n%s", prefix, res.stderr)
	}
	if strings.Count(strings.TrimRight(res.stderr, "\n"), "\n") != 0 {
		t.Errorf("esperava uma única linha em stderr, obtive:\n%s", res.stderr)
	}
	if res.stdout != "" {
		t.Errorf("nada deveria ir para stdout em caso de falha:\n%s", res.stdout)
	}
}

// 100 — $DATADIR necessário não existe.
func TestExit100_DataDirMissing(t *testing.T) {
	env := without(fullEnv(), "AGENT_UUID") // força leitura em $DATADIR
	env["DATADIR"] = filepath.Join(t.TempDir(), "nao-existe")
	checkFailure(t, run(t, env), 100, "DATADIR")
}

// 100 — $DATADIR aponta para um arquivo comum, não um diretório.
func TestExit100_DataDirIsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "arquivo")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := without(fullEnv(), "AGENT_UUID")
	env["DATADIR"] = file
	checkFailure(t, run(t, env), 100, "DATADIR")
}

// 100 — erro de I/O real ao ler um arquivo de $DATADIR não é mascarado como
// fonte vazia (SPEC §6).
func TestExit100_ReadIOError(t *testing.T) {
	skipIfRoot(t)
	env := without(withDataDir(t), "AGENT_UUID")
	target := filepath.Join(env["DATADIR"], "agent_uuid")
	if err := os.WriteFile(target, []byte("01900000-0000-7000-8000-000000000abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o644) })
	checkFailure(t, run(t, env), 100, "DATADIR")
}

// 102 — env MACHINE_ID não casa com ^[0-9a-f]{32}$.
func TestExit102_MachineIDInvalid(t *testing.T) {
	for _, bad := range []string{"nao-hex", "abcdef", strings.Repeat("f", 33), "ABCDEFG0123456789abcdef012345678"} {
		t.Run(bad, func(t *testing.T) {
			env := fullEnv()
			env["MACHINE_ID"] = bad
			checkFailure(t, run(t, env), 102, "MACHINE_ID")
		})
	}
}

// 103 — todas as fontes vazias e argv[0] saneado ficou vazio.
func TestExit103_Argv0Empty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("argv[0] vazio não é representável no Windows")
	}
	env := without(withDataDir(t), "AGENT_NAME")
	checkFailure(t, runArgv0(t, env, ""), 103, "AGENT_NAME")
}

// 104 — valor de AGENT_NAME reprovado na regex, vindo da env, do arquivo ou de
// argv[0]. Os três caminhos devem convergir para o mesmo código.
func TestExit104_AgentNameInvalid(t *testing.T) {
	t.Run("env", func(t *testing.T) {
		env := fullEnv()
		env["AGENT_NAME"] = "Nome Invalido!"
		checkFailure(t, run(t, env), 104, "AGENT_NAME")
	})

	t.Run("arquivo", func(t *testing.T) {
		env := without(withDataDir(t), "AGENT_NAME")
		writeFile(t, env["DATADIR"], "agent_name", "nome invalido!\n")
		checkFailure(t, run(t, env), 104, "AGENT_NAME")
	})

	t.Run("argv0", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("argv[0] arbitrário não é representável no Windows")
		}
		env := without(withDataDir(t), "AGENT_NAME")
		checkFailure(t, runArgv0(t, env, "/bin/nome invalido!"), 104, "AGENT_NAME")
	})
}

// 106 — gravação de $DATADIR/agent_uuid falhou.
func TestExit106_AgentUUIDWriteFailure(t *testing.T) {
	skipIfRoot(t)
	env := without(withDataDir(t), "AGENT_UUID")
	// machine_id vem da env para que a primeira gravação tentada seja a do
	// agent_uuid; do contrário o processo morreria antes, com 113.
	readOnly(t, env["DATADIR"])
	checkFailure(t, run(t, env), 106, "AGENT_UUID")
}

// 107 — valor de AGENT_UUID reprovado na regex de UUIDv7.
func TestExit107_AgentUUIDInvalid(t *testing.T) {
	for name, bad := range map[string]string{
		"curto demais":  "deadbeef",
		"versao 4":      "019e99e3-42f0-4882-9719-2305ff84949c",
		"variante ruim": "019e99e3-42f0-7882-c719-2305ff84949c",
		"com espacos":   "019e99e3 42f0 7882 9719 2305ff84949c",
		"sem os hifens": "019e99e342f0788297192305ff84949c",
	} {
		t.Run(name, func(t *testing.T) {
			env := fullEnv()
			env["AGENT_UUID"] = bad
			checkFailure(t, run(t, env), 107, "AGENT_UUID")
		})
	}
}

// 109 — valor de HOSTNAME reprovado na regex.
func TestExit109_HostnameInvalid(t *testing.T) {
	for _, bad := range []string{"host_com_underscore", "Host Com Espaco", "host:8080"} {
		t.Run(bad, func(t *testing.T) {
			env := fullEnv()
			env["HOSTNAME"] = bad
			checkFailure(t, run(t, env), 109, "HOSTNAME")
		})
	}
}

// 111 — valor de WORKSPACE reprovado na regex, vindo da env ou do arquivo.
func TestExit111_WorkspaceInvalid(t *testing.T) {
	t.Run("env", func(t *testing.T) {
		env := fullEnv()
		env["WORKSPACE"] = "Workspace Invalido!"
		checkFailure(t, run(t, env), 111, "WORKSPACE")
	})

	t.Run("arquivo", func(t *testing.T) {
		env := without(withDataDir(t), "WORKSPACE")
		writeFile(t, env["DATADIR"], "workspace", "workspace invalido!\n")
		checkFailure(t, run(t, env), 111, "WORKSPACE")
	})
}

// 112 — Initialize() chamado mais de uma vez.
func TestExit112_InitializeTwice(t *testing.T) {
	env := fullEnv()
	env["LOGHUB_IDENT_HELPER_TWICE"] = "1"
	res := run(t, env)
	if res.code != 112 {
		t.Fatalf("exit=%d (esperava 112)\nstderr:\n%s", res.code, res.stderr)
	}
	if !strings.Contains(res.stderr, "mais de uma vez") {
		t.Errorf("stderr não explica a causa:\n%s", res.stderr)
	}
	// A primeira chamada deve ter funcionado normalmente antes do aborto.
	if got := res.field(t, "AGENT_NAME"); got != "svc" {
		t.Errorf("AGENT_NAME = %q; a 1ª chamada deveria ter resolvido normalmente", got)
	}
}

// 113 — gravação de $DATADIR/machine_id falhou.
func TestExit113_MachineIDWriteFailure(t *testing.T) {
	skipIfRoot(t)
	env := without(withDataDir(t), "MACHINE_ID")
	readOnly(t, env["DATADIR"])
	checkFailure(t, run(t, env), 113, "MACHINE_ID")
}

// ----- utilitários -----

// readOnly torna o diretório somente-leitura e restaura a permissão ao final.
// O Cleanup é registrado DEPOIS do de t.TempDir, portanto roda ANTES dele
// (ordem LIFO) e a remoção do diretório temporário continua funcionando.
func readOnly(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod 0555 em %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// skipIfRoot pula testes baseados em permissão: o root ignora os bits de modo.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("teste baseado em permissões POSIX")
	}
	if os.Geteuid() == 0 {
		t.Skip("rodando como root: os bits de permissão não bloqueiam o acesso")
	}
}
