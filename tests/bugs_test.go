package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Testes de regressão dos defeitos catalogados em tasks/REPORT-01.md e
// tasks/REPORT-03.md.
//
// Cada teste nasceu como um par TestChar_/TestFix_: o TestChar_ afirmava o
// comportamento defeituoso e o TestFix_ o comportamento esperado após a
// correção. Com os defeitos corrigidos, os TestChar_ cumpriram seu papel e
// foram substituídos pelos TestFix_ correspondentes, agora permanentes — é
// deles a tarefa de impedir que qualquer um dos bugs volte.

// ----- BUG-01: leitura de arquivo sem limite de tamanho -----

// TestFix_BUG01_BoundedRead: $MACHINE_ID_FILE apontando para um FIFO sem
// escritor travava o boot para sempre, porque o os.ReadFile bloqueava no open.
// Hoje a fonte é descartada sem abrir o arquivo e a cadeia segue.
func TestFix_BUG01_BoundedRead(t *testing.T) {
	fifo := makeFIFO(t)

	env := withDataDir(t)
	env["MACHINE_ID_FILE"] = fifo
	delete(env, "MACHINE_ID")

	res := runTimeout(t, env, 10*time.Second)
	if res.code != 0 {
		t.Fatalf("exit=%d (esperava 0, caindo para o próximo nível)\nstderr:\n%s", res.code, res.stderr)
	}
	if !reMachineID.MatchString(res.field(t, "MACHINE_ID")) {
		t.Errorf("MACHINE_ID = %q", res.field(t, "MACHINE_ID"))
	}
}

// TestFix_BUG01_HugeFileRejected: um arquivo grande demais para conter uma
// identidade é recusado sem ser inteiramente carregado. Com /dev/zero o
// comportamento antigo consumia ~8,9 GB de RSS em 6 s.
func TestFix_BUG01_HugeFileRejected(t *testing.T) {
	env := withDataDir(t)
	delete(env, "MACHINE_ID")
	big := filepath.Join(env["DATADIR"], "gigante")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	// 8 MiB de lixo numa única linha: muito acima de qualquer identidade real.
	if _, err := f.Write([]byte(strings.Repeat("x", 8<<20))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	env["MACHINE_ID_FILE"] = big

	res := runTimeout(t, env, 10*time.Second)
	if res.code != 0 {
		t.Fatalf("exit=%d (esperava 0: fonte inválida deve cair para o próximo nível)\nstderr:\n%s", res.code, res.stderr)
	}
}

// ----- BUG-02: $DATADIR ausente torna fatais fallbacks determinísticos -----

// TestFix_BUG02_FallbacksSurviveMissingDataDir: a ausência de $DATADIR é
// "fonte ausente" na LEITURA, deixando a cadeia alcançar os fallbacks que a
// SPEC §7 e §11 prometem infalíveis. Só a GRAVAÇÃO exige o diretório.
func TestFix_BUG02_FallbacksSurviveMissingDataDir(t *testing.T) {
	env := without(fullEnv(), "WORKSPACE", "AGENT_NAME")
	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d (esperava 0)\nstderr:\n%s", res.code, res.stderr)
	}
	if got := res.field(t, "WORKSPACE"); got != "default" {
		t.Errorf("WORKSPACE = %q (esperava default)", got)
	}
	if got, want := res.field(t, "AGENT_NAME"), helperAgentName(t); got != want {
		t.Errorf("AGENT_NAME = %q (esperava %q)", got, want)
	}
}

// TestFix_BUG02_WriteStillRequiresDataDir garante que a correção não afrouxou o
// caso legítimo: gerar identidade sem onde persistí-la continua sendo falha.
func TestFix_BUG02_WriteStillRequiresDataDir(t *testing.T) {
	env := without(fullEnv(), "AGENT_UUID") // precisa gerar e gravar
	checkFailure(t, run(t, env), 100, "DATADIR")
}

// ----- BUG-03: rotação silenciosa de identidade -----

// TestFix_BUG03_RotationIsAnnounced: descartar uma identidade persistida é
// evento operacional, não detalhe de diagnóstico. Sem o aviso, um agente volta
// com outro machine_id depois de um kill -9 no momento errado e aparece no
// servidor Loghub como uma máquina nova, sem rastro para correlacionar.
//
// Os dois arquivos usam conteúdo TRUNCADO — o resíduo típico de uma gravação
// interrompida, e o único que continua inválido após o saneamento do BUG-09.
func TestFix_BUG03_RotationIsAnnounced(t *testing.T) {
	env := without(withDataDir(t), "MACHINE_ID", "AGENT_UUID")
	dir := env["DATADIR"]
	writeFile(t, dir, "machine_id", "1111222233334444\n")            // 16 de 32 hex
	writeFile(t, dir, "agent_uuid", "01900000-0000-7000-8000-000\n") // truncado

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr:\n%s", res.code, res.stderr)
	}
	for _, want := range []string{"machine_id", "agent_uuid"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("stderr não avisa sobre o descarte de %s:\n%s", want, res.stderr)
		}
	}
	if !strings.Contains(res.stderr, "lib-loghub-ident: aviso: ") {
		t.Errorf("o aviso deveria sair mesmo fora do modo debug:\n%s", res.stderr)
	}
}

// TestFix_BUG03_NulByteKeepsIdentity é a outra metade da correção: um byte NUL
// residual — o resíduo mais comum de uma gravação truncada — NÃO deve mais
// custar a identidade da máquina. Ele é aparado no saneamento (BUG-09), o valor
// é reconhecido como válido e o arquivo permanece intacto.
func TestFix_BUG03_NulByteKeepsIdentity(t *testing.T) {
	env := without(withDataDir(t), "MACHINE_ID")
	dir := env["DATADIR"]
	original := "11112222333344445555666677778888"
	writeFile(t, dir, "machine_id", original+"\x00")

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr:\n%s", res.code, res.stderr)
	}
	if got := res.field(t, "MACHINE_ID"); got != original {
		t.Errorf("MACHINE_ID = %q (esperava %q preservado)", got, original)
	}
	if res.stderr != "" {
		t.Errorf("nada a avisar: a identidade não foi descartada; stderr:\n%s", res.stderr)
	}
}

// ----- BUG-04: corrida entre processos na geração da identidade -----

// TestFix_BUG04_ConcurrentStartupAgrees: processos concorrentes sobre o mesmo
// $DATADIR vazio convergem para uma única identidade, e ela é a que ficou no
// disco. Antes, 3 de 12 rodadas terminavam com UUIDs distintos entre os
// processos e algum deles rodando com identidade diferente da persistida.
func TestFix_BUG04_ConcurrentStartupAgrees(t *testing.T) {
	if testing.Short() {
		t.Skip("teste de corrida: pulado em -short")
	}

	const rounds, procs = 10, 10
	for round := 0; round < rounds; round++ {
		env := without(withDataDir(t), "MACHINE_ID", "AGENT_UUID")
		dir := env["DATADIR"]

		var (
			mu    sync.Mutex
			seen  = map[string]int{}
			wg    sync.WaitGroup
			start = make(chan struct{})
		)
		for i := 0; i < procs; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start // largada simultânea
				res := run(t, env)
				mu.Lock()
				defer mu.Unlock()
				seen[res.field(t, "AGENT_UUID")]++
			}()
		}
		close(start)
		wg.Wait()

		if len(seen) != 1 {
			t.Fatalf("rodada %d: %d AGENT_UUIDs distintos entre %d processos: %v", round, len(seen), procs, seen)
		}
		onDisk := readIdentFile(t, dir, "agent_uuid")
		for uuid := range seen {
			if uuid != onDisk {
				t.Fatalf("rodada %d: processos usaram %s, disco tem %s", round, uuid, onDisk)
			}
		}
	}
}

// TestFix_BUG04_CorruptRegenerationAgrees cobre o OUTRO gatilho da mesma
// corrida, que o teste acima não alcança: o arquivo auto-gerido já EXISTE, mas
// com conteúdo inválido.
//
// É o cenário de recuperação mais provável, não uma borda exótica: um arquivo
// corrompido normalmente significa que a máquina caiu no meio de uma gravação,
// e é justamente aí que várias réplicas sobem juntas para se recuperar. Como o
// nome já está ocupado, a criação exclusiva não arbitra nada — cada processo
// gerava o seu valor e o gravava incondicionalmente, e só a última gravação
// sobrevivia no disco enquanto todos os outros seguiam com outra identidade na
// memória. Medido antes da correção: até 20 valores distintos entre 20
// processos, com 1/20 batendo com o disco.
func TestFix_BUG04_CorruptRegenerationAgrees(t *testing.T) {
	if testing.Short() {
		t.Skip("teste de corrida: pulado em -short")
	}

	for _, tc := range []struct {
		name, file, seed, field, env string
	}{
		{"machine_id", "machine_id", "deadbeef", "MACHINE_ID", "MACHINE_ID"},
		{"agent_uuid", "agent_uuid", "not-a-valid-uuid", "AGENT_UUID", "AGENT_UUID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const rounds, procs = 6, 12
			for round := 0; round < rounds; round++ {
				// A env do campo sai para forçar a cadeia até a regeneração; a
				// do outro campo fica, isolando um gatilho por vez.
				env := without(withDataDir(t), tc.env)
				dir := env["DATADIR"]
				writeFile(t, dir, tc.file, tc.seed+"\n")

				var (
					mu    sync.Mutex
					seen  = map[string]int{}
					wg    sync.WaitGroup
					start = make(chan struct{})
				)
				for i := 0; i < procs; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start // largada simultânea
						res := run(t, env)
						mu.Lock()
						defer mu.Unlock()
						seen[res.field(t, tc.field)]++
					}()
				}
				close(start)
				wg.Wait()

				if len(seen) != 1 {
					t.Fatalf("rodada %d: %d valores distintos de %s entre %d processos: %v",
						round, len(seen), tc.field, procs, seen)
				}
				onDisk := readIdentFile(t, dir, tc.file)
				for v := range seen {
					if v != onDisk {
						t.Fatalf("rodada %d: processos usaram %s, disco tem %s", round, v, onDisk)
					}
				}
				if onDisk == tc.seed {
					t.Fatalf("rodada %d: o conteúdo inválido não foi substituído", round)
				}
			}
		})
	}
}

// ----- BUG-05: permissão efetiva depende do umask -----

// TestFix_BUG05_PermIsAlways0644: a permissão da SPEC §7/§9 é uma garantia, não
// um teto. Sob umask 077 os arquivos saíam 0600 e um sidecar com outro UID
// perdia o acesso à identidade compartilhada.
func TestFix_BUG05_PermIsAlways0644(t *testing.T) {
	skipIfRoot(t)

	env := without(withDataDir(t), "MACHINE_ID", "AGENT_UUID")
	dir := env["DATADIR"]

	if res := runUmask(t, env, "077"); res.code != 0 {
		t.Fatalf("exit=%d\nstderr:\n%s", res.code, res.stderr)
	}
	for _, name := range []string{"machine_id", "agent_uuid"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Errorf("%s = %04o (esperava 0644)", name, got)
		}
	}
}

// ----- BUG-06: validação frouxa de AGENT_NAME e WORKSPACE -----

// TestFix_BUG06_RejectsTraversalAndOversize: ".." e valores sem teto de
// comprimento passavam nas duas regexes. Como AgentName e Workspace
// identificam agente e tenant, qualquer consumidor que os use para compor um
// caminho herdava uma travessia de diretório vinda de variável de ambiente.
func TestFix_BUG06_RejectsTraversalAndOversize(t *testing.T) {
	for _, bad := range []string{".", ".."} {
		t.Run("agent_name="+bad, func(t *testing.T) {
			env := fullEnv()
			env["AGENT_NAME"] = bad
			checkFailure(t, run(t, env), 104, "AGENT_NAME")
		})
		t.Run("workspace="+bad, func(t *testing.T) {
			env := fullEnv()
			env["WORKSPACE"] = bad
			checkFailure(t, run(t, env), 111, "WORKSPACE")
		})
	}

	t.Run("agent_name gigante", func(t *testing.T) {
		env := fullEnv()
		env["AGENT_NAME"] = strings.Repeat("a", 5000)
		checkFailure(t, run(t, env), 104, "AGENT_NAME")
	})

	t.Run("workspace gigante", func(t *testing.T) {
		env := fullEnv()
		env["WORKSPACE"] = strings.Repeat("a", 5000)
		checkFailure(t, run(t, env), 111, "WORKSPACE")
	})
}

// TestFix_BUG06_LimitDoesNotRejectRealNames guarda o outro lado: o teto de 64
// caracteres não pode recusar um nome de agente ou tenant plausível.
func TestFix_BUG06_LimitDoesNotRejectRealNames(t *testing.T) {
	for _, good := range []string{
		"svc",
		"loghub-agent.v2_br",
		"a.b-c_d.0123456789",
		strings.Repeat("a", 64), // exatamente no limite
	} {
		env := fullEnv()
		env["AGENT_NAME"] = good
		res := run(t, env)
		if res.code != 0 {
			t.Errorf("AGENT_NAME %q rejeitado: exit=%d\n%s", good, res.code, res.stderr)
			continue
		}
		if got := res.field(t, "AGENT_NAME"); got != good {
			t.Errorf("AGENT_NAME = %q (esperava %q)", got, good)
		}
	}
}

// ----- BUG-07: modo debug não cumpre a SPEC §12 -----

// TestFix_BUG07_DebugFollowsSpec: seis linhas, uma por campo, cada uma com a
// origem E o valor final. Antes eram cinco (faltava DATADIR), nenhuma trazia o
// valor, e todas eram descartadas justamente no caminho de falha — quando são
// a única pista que o operador tem.
func TestFix_BUG07_DebugFollowsSpec(t *testing.T) {
	env := fullEnv()
	env["LOGHUB_IDENT_DEBUG"] = "1"

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr:\n%s", res.code, res.stderr)
	}
	for field, value := range map[string]string{
		"DATADIR":    env["DATADIR"],
		"MACHINE_ID": "abcdef0123456789abcdef0123456789",
		"AGENT_NAME": "svc",
		"AGENT_UUID": "019e99e3-42f0-7882-9719-2305ff84949c",
		"HOSTNAME":   "node01",
		"WORKSPACE":  "prod",
	} {
		line := findDebugLine(res.stderr, field)
		if line == "" {
			t.Errorf("falta a linha de debug de %s", field)
			continue
		}
		if !strings.Contains(line, value) {
			t.Errorf("linha de %s não traz o valor final %q: %q", field, value, line)
		}
	}

	// No caminho de falha o diagnóstico do que já foi resolvido deve sobreviver.
	fail := without(fullEnv(), "AGENT_UUID")
	fail["LOGHUB_IDENT_DEBUG"] = "1"
	failRes := run(t, fail)
	if failRes.code != 100 {
		t.Fatalf("cenário mudou: exit=%d (esperava 100)", failRes.code)
	}
	if len(debugLines(failRes.stderr)) == 0 {
		t.Errorf("nenhuma linha de debug no caminho de falha:\n%s", failRes.stderr)
	}
	if findDebugLine(failRes.stderr, "MACHINE_ID") == "" {
		t.Errorf("o campo já resolvido antes da falha não aparece:\n%s", failRes.stderr)
	}
}

// ----- BUG-09: firstLine não removia BOM nem bytes de controle -----

// TestFix_BUG09_BOMIsStripped: um agent_name gravado por um editor do Windows
// — que põe BOM por padrão — derrubava o processo com 104.
func TestFix_BUG09_BOMIsStripped(t *testing.T) {
	env := without(withDataDir(t), "AGENT_NAME")
	writeFile(t, env["DATADIR"], "agent_name", "\ufeffsvc\n")

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr:\n%s", res.code, res.stderr)
	}
	if got := res.field(t, "AGENT_NAME"); got != "svc" {
		t.Errorf("AGENT_NAME = %q (esperava svc)", got)
	}
}

// ----- BUG-10: DATADIR relativo -----

// TestFix_BUG10_RelativeDataDirRejected: um DATADIR relativo fazia a identidade
// depender do diretório de trabalho — o mesmo serviço iniciado de outro lugar
// virava outro agente.
func TestFix_BUG10_RelativeDataDirRejected(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "dados"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := without(fullEnv(), "AGENT_UUID")
	env["DATADIR"] = "dados"
	checkFailure(t, runInDir(t, env, base), 100, "DATADIR")
}

// TestFix_BUG10_AbsoluteDataDirIsCwdIndependent é a consequência prática: com
// caminho absoluto, dois CWDs diferentes produzem a MESMA identidade.
func TestFix_BUG10_AbsoluteDataDirIsCwdIndependent(t *testing.T) {
	base := t.TempDir()
	dirA := filepath.Join(base, "a")
	dirB := filepath.Join(base, "b")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	env := without(withDataDir(t), "MACHINE_ID", "AGENT_UUID")
	resA := runInDir(t, env, dirA)
	resB := runInDir(t, env, dirB)
	if resA.code != 0 || resB.code != 0 {
		t.Fatalf("exits %d/%d\n%s%s", resA.code, resB.code, resA.stderr, resB.stderr)
	}
	if a, b := resA.field(t, "AGENT_UUID"), resB.field(t, "AGENT_UUID"); a != b {
		t.Errorf("identidades divergentes conforme o CWD: %s vs %s", a, b)
	}
}

// ----- BUG-11: HOSTNAME aceita valores que não são hostnames -----

// TestFix_BUG11_RejectsDegenerateHostnames: validação no espírito da RFC 1123 —
// sem rótulos vazios, sem hífen nas bordas, no máximo 253 caracteres.
func TestFix_BUG11_RejectsDegenerateHostnames(t *testing.T) {
	for _, bad := range []string{"-", "...", "-host-", ".", strings.Repeat("h", 300)} {
		t.Run(bad, func(t *testing.T) {
			env := fullEnv()
			env["HOSTNAME"] = bad
			checkFailure(t, run(t, env), 109, "HOSTNAME")
		})
	}

	t.Run("hostnames válidos continuam passando", func(t *testing.T) {
		for _, good := range []string{"node01", "debv.tmsoft.com.br", "a", "web-1.svc.cluster.local"} {
			env := fullEnv()
			env["HOSTNAME"] = good
			if res := run(t, env); res.code != 0 {
				t.Errorf("hostname válido %q rejeitado: exit=%d\n%s", good, res.code, res.stderr)
			}
		}
	})
}

// ----- BUG-13: DataDir não era limpo (filepath.Clean) -----

// TestFix_BUG13_DataDirCleaned: o valor público devolvido por DataDir() é
// normalizado, então "/data" e "/data//" não viram chaves distintas para o
// mesmo diretório rio abaixo.
func TestFix_BUG13_DataDirCleaned(t *testing.T) {
	dir := t.TempDir()
	env := fullEnv()
	env["DATADIR"] = dir + "//"
	env["MACHINE_ID_FILE"] = filepath.Join(dir, "sem-machine-id")

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	cleaned := filepath.Clean(dir + "//")
	if got := res.field(t, "DATADIR"); got != cleaned {
		t.Errorf("DATADIR = %q (esperava %q, limpo com filepath.Clean)", got, cleaned)
	}
}

// ----- BUG-14: env não passava por firstLine (inconsistência SPEC §5) -----

// TestFix_BUG14_FirstLineAppliedToEnv: as duas fontes do mesmo campo — env e
// arquivo — passam pelo mesmo pipeline de saneamento, como manda a SPEC §5.
func TestFix_BUG14_FirstLineAppliedToEnv(t *testing.T) {
	env := withDataDir(t)
	env["AGENT_NAME"] = "valid-name\nsecond-line"

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	if got := res.field(t, "AGENT_NAME"); got != "valid-name" {
		t.Errorf("AGENT_NAME = %q (esperava valid-name, extraído da 1ª linha)", got)
	}
}

// ----- BUG-15: Stat em MACHINE_ID_FILE engolia erros que não são "not found" -----

// TestFix_BUG15_StatErrorReported: a SPEC §7.2 troca para /etc/machine-id
// quando o arquivo apontado NÃO EXISTE. "Inexistente" não é "qualquer erro":
// um problema de permissão precisa ser reportado, não mascarado com outra
// identidade em silêncio.
func TestFix_BUG15_StatErrorReported(t *testing.T) {
	skipIfRoot(t)

	dir := t.TempDir()
	restricted := filepath.Join(dir, "sub")
	if err := os.Mkdir(restricted, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(restricted, 0o755) })

	env := without(withDataDir(t), "MACHINE_ID")
	env["MACHINE_ID_FILE"] = filepath.Join(restricted, "mid")

	checkFailure(t, run(t, env), 100, "MACHINE_ID_FILE")
}

// TestFix_BUG15_MissingFileStillFallsBack garante que a correção não quebrou a
// regra da SPEC §7.2 que ela refina.
func TestFix_BUG15_MissingFileStillFallsBack(t *testing.T) {
	env := without(withDataDir(t), "MACHINE_ID")
	env["MACHINE_ID_FILE"] = filepath.Join(t.TempDir(), "nao-existe")

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	if !reMachineID.MatchString(res.field(t, "MACHINE_ID")) {
		t.Errorf("MACHINE_ID = %q", res.field(t, "MACHINE_ID"))
	}
}

// ----- BUG-16: classes de caracteres ambíguas nas regexes -----

// TestFix_BUG16_CharacterClassesAreCorrect: ".-" e "_-" dentro de uma classe
// são tecnicamente operadores de range. As regexes deram lugar a validadores
// manuais (PERF-01); este teste fixa o conjunto de caracteres aceito.
func TestFix_BUG16_CharacterClassesAreCorrect(t *testing.T) {
	env := fullEnv()
	env["AGENT_NAME"] = "svc.v2_br-1"
	env["HOSTNAME"] = "host.domain-x.com"
	env["WORKSPACE"] = "tenant.sub-1"
	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	// O underscore vale só para AGENT_NAME; hostname e workspace o recusam.
	for field, code := range map[string]int{"HOSTNAME": 109, "WORKSPACE": 111} {
		bad := fullEnv()
		bad[field] = "com_underscore"
		checkFailure(t, run(t, bad), code, field)
	}
}

// ----- PERF-01: o pacote regexp custava ~379 KB e ~11,6 µs a todo importador -----

// TestFix_PERF01_NoRegexpDependency impede a volta do regexp à árvore de
// dependências. As cinco validações eram classes de caracteres simples, mas o
// custo de init (355 alocações) era pago por todo binário que importasse a
// biblioteca, mesmo o que nunca chama Initialize().
func TestFix_PERF01_NoRegexpDependency(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("toolchain go indisponível no PATH")
	}
	out, err := exec.Command("go", "list", "-deps", "github.com/patrickbrandao/go-loghub-ident").Output()
	if err != nil {
		t.Skipf("go list falhou: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "regexp" || dep == "regexp/syntax" {
			t.Errorf("a biblioteca voltou a depender de %q", dep)
		}
	}
}

// ----- utilitários -----

// debugLines extrai as linhas de diagnóstico de stderr.
func debugLines(stderr string) []string {
	var out []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "lib-loghub-ident: debug: ") {
			out = append(out, line)
		}
	}
	return out
}

// findDebugLine devolve a linha de debug de um campo, ou "" se ausente.
func findDebugLine(stderr, field string) string {
	for _, line := range debugLines(stderr) {
		if strings.Contains(line, field+":") {
			return line
		}
	}
	return ""
}

// makeFIFO cria um pipe nomeado sem escritor: qualquer open() para leitura
// bloqueia indefinidamente. É a forma segura de demonstrar leitura sem prazo,
// já que /dev/zero derrubaria a máquina de testes por consumo de memória.
func makeFIFO(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("FIFO POSIX indisponível")
	}
	mkfifo, err := exec.LookPath("mkfifo")
	if err != nil {
		t.Skip("mkfifo não encontrado no PATH")
	}
	path := filepath.Join(t.TempDir(), "fifo")
	if out, err := exec.Command(mkfifo, path).CombinedOutput(); err != nil {
		t.Skipf("mkfifo falhou: %v (%s)", err, out)
	}
	return path
}
