package loghubident

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
