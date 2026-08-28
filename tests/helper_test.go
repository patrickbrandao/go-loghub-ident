package tests

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	lhident "github.com/patrickbrandao/go-loghub-ident"
)

// envHelper, quando definida, faz o binário de teste agir como o "programa
// cliente": chama Initialize() e imprime a identidade resolvida.
const envHelper = "LOGHUB_IDENT_HELPER"

// TestMain intercepta a execução em modo helper antes de rodar a suíte.
func TestMain(m *testing.M) {
	if os.Getenv(envHelper) != "" {
		runHelper()
		return // inalcançável: runHelper sempre encerra o processo
	}
	os.Exit(m.Run())
}

// runHelper reproduz o que um main() consumidor faria. Initialize() encerra o
// processo com o código da falha; se voltar, imprimimos os seis campos em
// formato "CHAVE=valor", uma linha por campo.
func runHelper() {
	lhident.Initialize()

	fmt.Printf("DATADIR=%s\n", lhident.DataDir())
	fmt.Printf("MACHINE_ID=%s\n", lhident.MachineID())
	fmt.Printf("AGENT_NAME=%s\n", lhident.AgentName())
	fmt.Printf("AGENT_UUID=%s\n", lhident.AgentUUID())
	fmt.Printf("HOSTNAME=%s\n", lhident.Hostname())
	fmt.Printf("WORKSPACE=%s\n", lhident.Workspace())

	// Uma segunda chamada deve encerrar com 112. Só é exercitada quando o
	// teste pede explicitamente, para não afetar os demais casos.
	if os.Getenv("LOGHUB_IDENT_HELPER_TWICE") != "" {
		lhident.Initialize()
		fmt.Println("ERRO=segunda chamada retornou em vez de encerrar")
	}
	os.Exit(0)
}

// result guarda o desfecho de uma execução do helper.
type result struct {
	code   int               // código de saída do processo
	fields map[string]string // campos "CHAVE=valor" lidos do stdout
	stdout string
	stderr string
}

// field devolve o valor de um campo do stdout, falhando o teste se ausente.
func (r result) field(t *testing.T, key string) string {
	t.Helper()
	v, ok := r.fields[key]
	if !ok {
		t.Fatalf("campo %q ausente na saída (exit=%d)\nstdout:\n%s\nstderr:\n%s",
			key, r.code, r.stdout, r.stderr)
	}
	return v
}

// options ajusta detalhes da execução do helper.
type options struct {
	limit    time.Duration // prazo máximo; 0 usa o padrão
	argv0    string        // sobrescreve argv[0] do subprocesso
	setArgv0 bool
	dir      string // diretório de trabalho do subprocesso
	umask    string // se != "", executa via "sh -c \'umask N; exec ...\'"
}

// run executa o binário de teste em modo helper com um ambiente LIMPO acrescido
// de env. Ambiente limpo é essencial: HOSTNAME, DATADIR e afins presentes na
// máquina de quem roda os testes contaminariam a resolução.
func run(t *testing.T, env map[string]string) result {
	t.Helper()
	return runOpts(t, env, options{})
}

// runTimeout é run com prazo máximo. Um estouro de prazo devolve code = -1.
func runTimeout(t *testing.T, env map[string]string, limit time.Duration) result {
	t.Helper()
	return runOpts(t, env, options{limit: limit})
}

// runArgv0 executa o helper com um argv[0] arbitrário — inclusive vazio, o
// único caminho que alcança o código de saída 103.
func runArgv0(t *testing.T, env map[string]string, argv0 string) result {
	t.Helper()
	return runOpts(t, env, options{argv0: argv0, setArgv0: true})
}

// runUmask executa o helper sob um umask específico (ex.: "077"), o único jeito
// de observar a permissão efetiva dos arquivos gravados em $DATADIR.
func runUmask(t *testing.T, env map[string]string, umask string) result {
	t.Helper()
	return runOpts(t, env, options{umask: umask})
}

// runInDir executa o helper com um diretório de trabalho específico, para
// exercitar caminhos relativos em DATADIR.
func runInDir(t *testing.T, env map[string]string, dir string) result {
	t.Helper()
	return runOpts(t, env, options{dir: dir})
}

// runBench é o harness para os benchmarks. Existe para que eles não precisem
// fabricar um &testing.T{} na mão: esse valor não é projetado para construção
// direta e um t.FailNow interno encerraria apenas a goroutine, deixando o
// benchmark seguir com um resultado sem sentido.
func runBench(b *testing.B, env map[string]string) (code int, stderr string) {
	b.Helper()
	res := runOpts(b, env, options{})
	return res.code, res.stderr
}

func runOpts(t testing.TB, env map[string]string, opt options) result {
	t.Helper()

	limit := opt.limit
	if limit == 0 {
		limit = 60 * time.Second
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable(): %v", err)
	}

	cmd := exec.Command(exe)
	if opt.setArgv0 {
		cmd.Args = []string{opt.argv0}
	}
	if opt.umask != "" {
		// O umask é herdado, não configurável por env: só um shell intermediário
		// consegue alterá-lo antes do exec.
		cmd = exec.Command("/bin/sh", "-c", "umask "+opt.umask+`; exec "$0"`, exe)
	}
	cmd.Dir = opt.dir
	cmd.Env = append([]string{envHelper + "=1"}, flatten(env)...)
	if dir := coverDir(t); dir != "" {
		// Sob "go test -cover" o subprocesso também é instrumentado. Sem
		// GOCOVERDIR ele despeja um aviso em stderr — que contaminaria os
		// testes que inspecionam a saída de erro. Com a variável definida, o
		// aviso some E os contadores do subprocesso são preservados; para
		// somá-los ao perfil principal use:
		//
		//	go tool covdata textfmt -i=<dir> -o=cover.out
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+dir)
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start do helper: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	res := result{code: -1}
	select {
	case err := <-done:
		res.code = exitCode(t, err)
	case <-time.After(limit):
		_ = cmd.Process.Kill()
		<-done
		res.code = -1 // sentinela de "travou"
	}

	res.stdout = stdout.String()
	res.stderr = stripToolchainWarnings(stderr.String())
	res.fields = parseFields(res.stdout)
	return res
}

// coverDirOnce protege a criação do diretório de cobertura dos subprocessos.
var (
	coverDirOnce sync.Once
	coverDirPath string
)

// coverDir devolve o diretório de cobertura dos subprocessos, ou "" quando a
// bateria não está rodando sob -cover.
func coverDir(t testing.TB) string {
	t.Helper()
	if testing.CoverMode() == "" {
		return ""
	}
	coverDirOnce.Do(func() {
		// Um GOCOVERDIR já definido pelo desenvolvedor tem precedência.
		if dir := os.Getenv("GOCOVERDIR"); dir != "" {
			coverDirPath = dir
			return
		}
		dir, err := os.MkdirTemp("", "loghub-ident-cover-")
		if err != nil {
			t.Logf("não foi possível criar o diretório de cobertura: %v", err)
			return
		}
		coverDirPath = dir
		fmt.Fprintf(os.Stderr, "cobertura dos subprocessos em %s (some com: go tool covdata textfmt -i=%s -o=cover.out)\n", dir, dir)
	})
	return coverDirPath
}

// stripToolchainWarnings remove da saída de erro as mensagens emitidas pelo
// runtime de cobertura do Go, que não fazem parte do contrato da biblioteca.
func stripToolchainWarnings(stderr string) string {
	if !strings.Contains(stderr, "warning: GOCOVERDIR") {
		return stderr
	}
	var kept []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "warning: GOCOVERDIR") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// exitCode extrai o código de saída do erro devolvido por cmd.Wait.
func exitCode(t testing.TB, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	t.Fatalf("erro inesperado ao esperar o helper: %v", err)
	return -1
}

// flatten converte o mapa de env em "CHAVE=valor".
func flatten(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// parseFields lê as linhas "CHAVE=valor" impressas pelo helper.
func parseFields(out string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			fields[k] = v
		}
	}
	return fields
}

// ----- Utilitários de ambiente -----

// fullEnv devolve um ambiente com os cinco campos vindos de env e um DATADIR
// inexistente: é o "caminho feliz" da SPEC §6, em que o disco nunca é tocado.
func fullEnv() map[string]string {
	return map[string]string{
		"DATADIR":    filepath.Join(os.TempDir(), "loghub-ident-inexistente"),
		"MACHINE_ID": "abcdef0123456789abcdef0123456789",
		"AGENT_NAME": "svc",
		"AGENT_UUID": "019e99e3-42f0-7882-9719-2305ff84949c",
		"HOSTNAME":   "node01",
		"WORKSPACE":  "prod",
	}
}

// withDataDir devolve fullEnv apontando para um diretório temporário real.
func withDataDir(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	env := fullEnv()
	env["DATADIR"] = dir
	// Um MACHINE_ID_FILE inexistente neutraliza o /etc/machine-id da máquina
	// que roda os testes, tornando os casos determinísticos.
	env["MACHINE_ID_FILE"] = filepath.Join(dir, "sem-machine-id")
	return env
}

// without remove chaves do ambiente, forçando a cadeia de fallback.
func without(env map[string]string, keys ...string) map[string]string {
	for _, k := range keys {
		delete(env, k)
	}
	return env
}

// helperAgentName devolve o AGENT_NAME que o fallback argv[0] deve produzir
// para o binário de teste: base do executável, lowercase, sem sufixo ".exe".
func helperAgentName(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable(): %v", err)
	}
	return strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe")
}

// readIdentFile lê um arquivo auto-gerido de $DATADIR, já sem o "\n" final.
func readIdentFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("leitura de %s: %v", name, err)
	}
	return strings.TrimSpace(string(data))
}
