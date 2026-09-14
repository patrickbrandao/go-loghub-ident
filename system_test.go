package loghubident

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Testes de osSystem — a camada que fala com o sistema operacional de verdade.
// O fakeSystem cobre a lógica de resolução; aqui exercitamos as garantias que
// só o filesystem real pode dar: limite de leitura, exclusão mútua na criação e
// substituição atômica.

// ----- ReadFile (BUG-01) -----

func TestOSSystem_ReadFile(t *testing.T) {
	dir := t.TempDir()
	sys := osSystem{}

	t.Run("arquivo comum", func(t *testing.T) {
		path := filepath.Join(dir, "ok")
		if err := os.WriteFile(path, []byte("conteudo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		data, err := sys.ReadFile(path)
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		if string(data) != "conteudo\n" {
			t.Errorf("conteúdo = %q", data)
		}
	})

	t.Run("ausente devolve ErrNotExist", func(t *testing.T) {
		_, err := sys.ReadFile(filepath.Join(dir, "nao-existe"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v (esperava fs.ErrNotExist)", err)
		}
	})

	t.Run("diretório é fonte inválida", func(t *testing.T) {
		_, err := sys.ReadFile(dir)
		if !errors.Is(err, errInvalidSource) {
			t.Errorf("err = %v (esperava errInvalidSource)", err)
		}
	})

	t.Run("acima do limite é fonte inválida", func(t *testing.T) {
		path := filepath.Join(dir, "gigante")
		if err := os.WriteFile(path, make([]byte, maxIdentFileSize+1), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := sys.ReadFile(path)
		if !errors.Is(err, errInvalidSource) {
			t.Errorf("err = %v (esperava errInvalidSource)", err)
		}
	})

	t.Run("exatamente no limite é lido", func(t *testing.T) {
		path := filepath.Join(dir, "no-limite")
		if err := os.WriteFile(path, make([]byte, maxIdentFileSize), 0o644); err != nil {
			t.Fatal(err)
		}
		data, err := sys.ReadFile(path)
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		if len(data) != maxIdentFileSize {
			t.Errorf("len = %d (esperava %d)", len(data), maxIdentFileSize)
		}
	})
}

// ----- ReadFileNoFollow (001-EXFILTRACAO-VIA-SYMLINK-EM-DATADIR) -----

func TestOSSystem_ReadFileNoFollow(t *testing.T) {
	dir := t.TempDir()
	sys := osSystem{}

	t.Run("arquivo comum é lido normalmente", func(t *testing.T) {
		path := filepath.Join(dir, "comum")
		if err := os.WriteFile(path, []byte("secreto-proprio\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		data, err := sys.ReadFileNoFollow(path)
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		if string(data) != "secreto-proprio\n" {
			t.Errorf("conteúdo = %q", data)
		}
	})

	t.Run("symlink para arquivo comum é recusado por ReadFileNoFollow mas aceito por ReadFile", func(t *testing.T) {
		target := filepath.Join(dir, "alvo")
		if err := os.WriteFile(target, []byte("alvo-externo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link-alvo")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}

		// ReadFile (fontes do sistema) deve seguir o symlink
		dataFollow, err := sys.ReadFile(link)
		if err != nil {
			t.Fatalf("ReadFile falhou ao seguir symlink: %v", err)
		}
		if string(dataFollow) != "alvo-externo\n" {
			t.Errorf("ReadFile = %q (esperava alvo-externo\\n)", dataFollow)
		}

		// ReadFileNoFollow (arquivos de $DATADIR) DEVE recusar o symlink
		_, err = sys.ReadFileNoFollow(link)
		if err == nil {
			t.Fatal("ReadFileNoFollow aceitou symlink inesperadamente")
		}
		if !errors.Is(err, errInvalidSource) {
			t.Fatalf("err = %v (esperava errInvalidSource)", err)
		}
		if !strings.Contains(err.Error(), "link simbólico") {
			t.Errorf("mensagem de erro = %q (esperava conter 'link simbólico')", err.Error())
		}
	})
}

// ----- CreateExclusive e o seu plano B (BUG-04, BUG-05, BUG-17, 003-PLANO-B) -----

// createExclusive reúne as duas implementações para que ambas passem pela mesma
// bateria. O plano B roda quando linkFile falha (simulando filesystem sem hard link).
var createExclusive = map[string]func(string, []byte, os.FileMode) (bool, error){
	"link": osSystem{}.CreateExclusive,
	"direct": func(path string, data []byte, perm os.FileMode) (bool, error) {
		oldLink := linkFile
		defer func() { linkFile = oldLink }()
		linkFile = func(oldname, newname string) error {
			return errors.New("operation not supported (fake ENOSYS)")
		}
		return osSystem{}.CreateExclusive(path, data, perm)
	},
}

func TestCreateExclusive(t *testing.T) {
	for name, create := range createExclusive {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "machine_id")

			created, err := create(path, []byte("primeiro\n"), filePerm)
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if !created {
				t.Fatal("created = false na primeira criação")
			}

			// O conteúdo precisa estar COMPLETO assim que o arquivo existe:
			// um leitor concorrente nunca pode enxergar um arquivo vazio.
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "primeiro\n" {
				t.Errorf("conteúdo = %q", data)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			// No Windows os bits de permissão Unix não existem: Perm() reflete só o
			// atributo somente-leitura (0666 ou 0444).
			if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != filePerm {
				t.Errorf("permissão = %04o (esperava %04o)", got, filePerm)
			}

			// Segunda tentativa: o arquivo já existe, então perdemos a corrida
			// e o conteúdo do vencedor fica intacto.
			created, err = create(path, []byte("segundo\n"), filePerm)
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if created {
				t.Error("created = true sobre arquivo existente")
			}
			data, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "primeiro\n" {
				t.Errorf("o valor do vencedor foi sobrescrito: %q", data)
			}

			assertNoTempLeft(t, dir)
		})
	}
}

func TestCreateExclusive_WriteFailureIsReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows: o atributo somente-leitura do diretório não impede criar arquivos nele")
	}
	if os.Geteuid() == 0 {
		t.Skip("rodando como root: os bits de permissão não bloqueiam a escrita")
	}
	for name, create := range createExclusive {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

			if _, err := create(filepath.Join(dir, "machine_id"), []byte("x\n"), filePerm); err == nil {
				t.Error("esperava erro ao gravar em diretório somente-leitura")
			}
		})
	}
}

// ----- ReplaceFile (BUG-17, IMP-12) -----

func TestReplaceFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent_uuid")
	if err := os.WriteFile(path, []byte("corrompido"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (osSystem{}).ReplaceFile(path, []byte("novo\n"), filePerm); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "novo\n" {
		t.Errorf("conteúdo = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A permissão é corrigida mesmo quando o arquivo já existia com outra.
	// No Windows os bits de permissão Unix não existem: Perm() reflete só o
	// atributo somente-leitura (0666 ou 0444).
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != filePerm {
		t.Errorf("permissão = %04o (esperava %04o)", got, filePerm)
	}
	assertNoTempLeft(t, dir)
}

func TestReplaceFile_CreatesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "machine_id")
	if err := (osSystem{}).ReplaceFile(path, []byte("valor\n"), filePerm); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "valor\n" {
		t.Errorf("conteúdo = %q, err = %v", data, err)
	}
	assertNoTempLeft(t, dir)
}

// assertNoTempLeft garante que nenhuma gravação deixou arquivo temporário para
// trás em $DATADIR.
func assertNoTempLeft(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("arquivo temporário deixado para trás: %s", e.Name())
		}
	}
}

// ----- Repasses diretos ao sistema operacional -----

func TestOSSystem_Passthroughs(t *testing.T) {
	sys := osSystem{}

	t.Setenv("LOGHUB_IDENT_TEST_ENV", "valor")
	if got := sys.Getenv("LOGHUB_IDENT_TEST_ENV"); got != "valor" {
		t.Errorf("Getenv = %q", got)
	}
	if got := sys.Getenv("LOGHUB_IDENT_TEST_ENV_AUSENTE"); got != "" {
		t.Errorf("Getenv(ausente) = %q", got)
	}

	dir := t.TempDir()
	info, err := sys.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Errorf("Stat(dir) = %v, %v", info, err)
	}
	if _, err := sys.Stat(filepath.Join(dir, "nao-existe")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(ausente) = %v (esperava fs.ErrNotExist)", err)
	}

	path := filepath.Join(dir, "apagar")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sys.Remove(path); err != nil {
		t.Errorf("Remove = %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("arquivo ainda existe após Remove: %v", err)
	}
	if err := sys.Remove(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove(ausente) = %v (esperava fs.ErrNotExist)", err)
	}

	want, werr := os.Hostname()
	got, gerr := sys.Hostname()
	if got != want || (gerr == nil) != (werr == nil) {
		t.Errorf("Hostname = %q, %v (os.Hostname = %q, %v)", got, gerr, want, werr)
	}

	args := sys.Args()
	if len(args) != len(os.Args) {
		t.Fatalf("Args tem %d elementos; os.Args tem %d", len(args), len(os.Args))
	}
	for i := range args {
		if args[i] != os.Args[i] {
			t.Errorf("Args[%d] = %q (esperava %q)", i, args[i], os.Args[i])
		}
	}
}

// GenerateUUIDv7 é o ponto de contato com a biblioteca de UUID: o que ela
// devolve precisa passar pelos validadores desta biblioteca sem retoque, para
// AGENT_UUID (canônico) e para MACHINE_ID (sem hífens).
func TestOSSystem_GenerateUUIDv7(t *testing.T) {
	sys := osSystem{}
	const n = 10_000
	seen := make(map[string]struct{}, n)
	now := time.Now().UnixMilli()
	for i := 0; i < n; i++ {
		u, err := sys.GenerateUUIDv7()
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		if !validAgentUUID(u) {
			t.Fatalf("%q não é um UUIDv7 canônico aceito por validAgentUUID", u)
		}
		if mid := normalizeMachineID(u); !validMachineID(mid) {
			t.Fatalf("%q não vira um MACHINE_ID válido (%q)", u, mid)
		}
		if _, dup := seen[u]; dup {
			t.Fatalf("UUID repetido após %d gerações: %s", i, u)
		}
		seen[u] = struct{}{}

		// Os 48 bits iniciais são milissegundos Unix: precisam apontar para agora.
		ms, err := strconv.ParseInt(u[:8]+u[9:13], 16, 64)
		if err != nil {
			t.Fatalf("timestamp de %q ilegível: %v", u, err)
		}
		if diff := ms - now; diff < -60_000 || diff > 60_000 {
			t.Fatalf("timestamp de %q está %d ms distante do relógio", u, diff)
		}
	}
}

// ----- Primitivas de gravação: os ramos de erro -----

func TestSyncDir_BestEffort(t *testing.T) {
	// Nenhum dos dois pode entrar em pânico ou travar: a sincronização do
	// diretório é opcional por contrato.
	syncDir(filepath.Join(t.TempDir(), "nao-existe"))
	syncDir(t.TempDir())
}

func TestWriteSyncClose_WriteFailureIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "somente-leitura")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path) // descritor só de leitura: o Write falha
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSyncClose(f, []byte("dados\n"), filePerm); err == nil {
		t.Fatal("esperava erro ao gravar num descritor somente-leitura")
	}
	// O descritor é fechado pela própria função, mesmo no erro.
	if err := f.Close(); err == nil {
		t.Error("esperava erro ao fechar um descritor que writeSyncClose já fechou")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "x" {
		t.Errorf("conteúdo original alterado: %q, %v", data, err)
	}
}

func TestReplaceFile_RenameFailureCleansTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sou-um-diretorio")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	// O rename de um arquivo sobre um diretório falha; o temporário não pode
	// ficar para trás nem o diretório sumir.
	if err := (osSystem{}).ReplaceFile(target, []byte("novo\n"), filePerm); err == nil {
		t.Fatal("esperava erro ao substituir um diretório por arquivo")
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Errorf("o diretório alvo foi alterado: %v, %v", info, err)
	}
	assertNoTempLeft(t, dir)
}

// Sem o diretório pai não há como gravar o temporário: as duas primitivas
// devolvem o erro em vez de criar o caminho ou falhar em silêncio. Vale em
// qualquer plataforma, ao contrário do diretório somente-leitura.
func TestWrites_MissingParentDirIsReported(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nao-existe", "machine_id")
	for name, create := range createExclusive {
		if _, err := create(missing, []byte("x\n"), filePerm); err == nil {
			t.Errorf("%s: esperava erro sem o diretório pai", name)
		}
	}
	if err := (osSystem{}).ReplaceFile(missing, []byte("x\n"), filePerm); err == nil {
		t.Error("ReplaceFile: esperava erro sem o diretório pai")
	}
	if _, err := os.Lstat(filepath.Dir(missing)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("o diretório pai não deveria ter sido criado: %v", err)
	}
}

// ----- Plano B: o ciclo de vida da reivindicação (.claim) -----

// shortClaims encurta o TTL e o intervalo de espera do plano B para que os
// cenários abaixo rodem em milissegundos em vez de dezenas de segundos.
func shortClaims(t *testing.T) {
	t.Helper()
	oldTTL, oldPoll := claimTTL, claimPoll
	claimTTL, claimPoll = 150*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { claimTTL, claimPoll = oldTTL, oldPoll })
}

func claimPaths(t *testing.T) (dir, path, claim string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "machine_id")
	return dir, path, path + ".claim"
}

// Uma reivindicação mais velha que o TTL é de um processo que morreu entre
// reivindicar e publicar: é descartada e a criação segue.
func TestCreateExclusiveDirect_StaleClaimIsTakenOver(t *testing.T) {
	shortClaims(t)
	dir, path, claim := claimPaths(t)
	if err := os.WriteFile(claim, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(claim, old, old); err != nil {
		t.Fatal(err)
	}

	created, err := createExclusive["direct"](path, []byte("novo\n"), filePerm)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v (esperava criar sobre a reivindicação obsoleta)", created, err)
	}
	if data, _ := os.ReadFile(path); string(data) != "novo\n" {
		t.Errorf("conteúdo = %q", data)
	}
	if _, err := os.Lstat(claim); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a reivindicação deveria ter sido liberada: %v", err)
	}
	assertNoTempLeft(t, dir)
}

// Reivindicação viva de outro processo E destino já publicado: perdemos a
// corrida na hora, sem esperar, e a reivindicação alheia fica intacta.
func TestCreateExclusiveDirect_TargetAlreadyPublished(t *testing.T) {
	shortClaims(t)
	dir, path, claim := claimPaths(t)
	if err := os.WriteFile(path, []byte("vencedor\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claim, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	created, err := createExclusive["direct"](path, []byte("perdedor\n"), filePerm)
	if err != nil || created {
		t.Fatalf("created=%v err=%v (esperava perder sem erro)", created, err)
	}
	if data, _ := os.ReadFile(path); string(data) != "vencedor\n" {
		t.Errorf("o valor do vencedor foi sobrescrito: %q", data)
	}
	if _, err := os.Lstat(claim); err != nil {
		t.Errorf("a reivindicação de outro processo foi removida: %v", err)
	}
	assertNoTempLeft(t, dir)
}

// Sem reivindicação, mas com o destino já publicado: a reivindicação é obtida,
// o destino é detectado e nada é renomeado por cima dele.
func TestCreateExclusiveDirect_ClaimTakenButTargetAppeared(t *testing.T) {
	shortClaims(t)
	dir, path, claim := claimPaths(t)
	if err := os.WriteFile(path, []byte("vencedor\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := createExclusive["direct"](path, []byte("perdedor\n"), filePerm)
	if err != nil || created {
		t.Fatalf("created=%v err=%v (esperava perder sem erro)", created, err)
	}
	if data, _ := os.ReadFile(path); string(data) != "vencedor\n" {
		t.Errorf("o valor do vencedor foi sobrescrito: %q", data)
	}
	if _, err := os.Lstat(claim); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a nossa reivindicação deveria ter sido liberada: %v", err)
	}
	assertNoTempLeft(t, dir)
}

// Reivindicação viva de outro processo que ainda vai publicar: esperamos o
// destino aparecer e adotamos, em vez de duplicar a identidade.
func TestCreateExclusiveDirect_WaitsForHolderToPublish(t *testing.T) {
	shortClaims(t)
	dir, path, claim := claimPaths(t)
	if err := os.WriteFile(claim, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	published := make(chan struct{})
	go func() {
		defer close(published)
		time.Sleep(40 * time.Millisecond)
		_ = os.WriteFile(path, []byte("vencedor\n"), 0o644)
		_ = os.Remove(claim)
	}()

	created, err := createExclusive["direct"](path, []byte("perdedor\n"), filePerm)
	<-published
	if err != nil || created {
		t.Fatalf("created=%v err=%v (esperava adotar o vencedor)", created, err)
	}
	if data, _ := os.ReadFile(path); string(data) != "vencedor\n" {
		t.Errorf("conteúdo = %q", data)
	}
	assertNoTempLeft(t, dir)
}

// Reivindicação viva que nunca publica (o dono renova o mtime, mas não
// termina): o plano B não espera para sempre e devolve erro no prazo.
func TestCreateExclusiveDirect_GivesUpOnLiveClaimWithoutTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("espera ~1 s pelo prazo da reivindicação")
	}
	shortClaims(t) // prazo = claimTTL + 1 s
	dir, path, claim := claimPaths(t)
	if err := os.WriteFile(claim, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				now := time.Now()
				_ = os.Chtimes(claim, now, now) // o dono continua vivo
			}
		}
	}()

	_, err := createExclusive["direct"](path, []byte("x\n"), filePerm)
	close(stop)
	<-stopped
	if err == nil {
		t.Fatal("esperava erro ao estourar o prazo da reivindicação")
	}
	if !strings.Contains(err.Error(), "não liberada") {
		t.Errorf("erro = %v (esperava menção à reivindicação não liberada)", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("o destino não deveria existir: %v", err)
	}
	assertNoTempLeft(t, dir)
}
