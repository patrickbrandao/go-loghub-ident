package loghubident

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	loghubuuid "github.com/patrickbrandao/go-loghub-uuid"
)

// maxIdentFileSize limita a leitura de qualquer fonte de identidade. Nenhum
// campo passa de ~40 bytes; 4 KiB é folga generosa e converte um arquivo
// hostil (FIFO, /dev/zero, log gigante apontado por engano em MACHINE_ID_FILE)
// em "fonte inválida" em vez de OOM ou travamento no boot.
const maxIdentFileSize = 4 << 10

// errInvalidSource marca uma fonte que existe, mas não pode conter uma
// identidade: não é arquivo comum (FIFO, device, diretório) ou é grande demais.
// A cadeia de resolução trata esse erro como "fonte ausente" e segue para o
// próximo nível — ao contrário de um erro de I/O legítimo, que aborta.
var errInvalidSource = errors.New("fonte de identidade inválida")

// system abstrai todo o acesso ao sistema operacional usado durante a
// resolução da identidade. A separação por interface permite que os testes
// injetem uma implementação falsa (em memória), tornando a lógica de
// resolução 100% testável sem tocar no ambiente, no disco ou no hostname reais.
//
// Em produção, a implementação concreta é osSystem.
type system interface {
	// Getenv retorna o valor da variável de ambiente, ou "" se ausente.
	Getenv(key string) string

	// Stat retorna informações do caminho (usado para validar o DATADIR).
	Stat(path string) (os.FileInfo, error)

	// ReadFile lê o conteúdo de um arquivo, limitado a maxIdentFileSize.
	// Devolve um erro que embrulha errInvalidSource se o caminho não for um
	// arquivo comum ou exceder o limite.
	ReadFile(path string) ([]byte, error)

	// CreateExclusive grava data em path SOMENTE se path ainda não existir.
	// Devolve (true, nil) se ESTE processo criou o arquivo e (false, nil) se
	// outro chegou antes — deixando o filesystem arbitrar a corrida entre
	// processos irmãos que sobem ao mesmo tempo sobre o mesmo $DATADIR.
	//
	// O conteúdo é durável (fsync) e a permissão é aplicada explicitamente,
	// sem interferência do umask, antes de o arquivo se tornar visível.
	CreateExclusive(path string, data []byte, perm os.FileMode) (bool, error)

	// ReplaceFile substitui o conteúdo de path de forma atômica (arquivo
	// temporário + rename), com a mesma garantia de durabilidade e permissão
	// de CreateExclusive. Nunca expõe conteúdo parcial a um leitor
	// concorrente, nem deixa um arquivo truncado se o processo morrer no meio.
	ReplaceFile(path string, data []byte, perm os.FileMode) error

	// Remove apaga um arquivo. A ausência do arquivo não é erro para o
	// chamador, que trata fs.ErrNotExist como sucesso.
	Remove(path string) error

	// Hostname retorna o nome de host do sistema.
	Hostname() (string, error)

	// Args retorna os argumentos do processo (equivalente a os.Args).
	Args() []string

	// GenerateUUIDv7 gera um UUIDv7 canônico (com hífens).
	//
	// A assinatura inclui error para permitir que os testes simulem uma
	// falha de geração (códigos de saída 105 e 114). A implementação real
	// nunca retorna erro, pois loghubuuid.GenerateString não falha.
	GenerateUUIDv7() (string, error)
}

// osSystem é a implementação de system baseada no sistema operacional real.
// É um struct vazio e sem estado, seguro para uso concorrente.
type osSystem struct{}

func (osSystem) Getenv(key string) string { return os.Getenv(key) }

func (osSystem) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }

// ReadFile lê no máximo maxIdentFileSize bytes de um arquivo COMUM.
func (osSystem) ReadFile(path string) ([]byte, error) {
	// Stat NÃO abre o arquivo: um FIFO sem escritor é detectado sem bloquear,
	// o que seria impossível se abríssemos primeiro.
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s não é um arquivo comum (%s)",
			errInvalidSource, path, info.Mode().Type())
	}
	if info.Size() > maxIdentFileSize {
		return nil, fmt.Errorf("%w: %s tem %d bytes, acima do limite de %d",
			errInvalidSource, path, info.Size(), maxIdentFileSize)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// O LimitReader cobre a janela TOCTOU entre o Stat e o Open.
	return io.ReadAll(io.LimitReader(f, maxIdentFileSize))
}

// CreateExclusive materializa o arquivo já completo e com a permissão certa,
// via arquivo temporário + os.Link. O link é atômico em POSIX e falha com
// ErrExist se o destino já existir, então nenhum leitor concorrente enxerga um
// arquivo recém-criado e ainda vazio — janela que existiria com
// O_CREATE|O_EXCL seguido de Write.
func (osSystem) CreateExclusive(path string, data []byte, perm os.FileMode) (bool, error) {
	tmp, err := writeTemp(path, data, perm)
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp) // no caminho feliz o link já criou o destino

	switch err := os.Link(tmp, path); {
	case err == nil:
		syncDir(filepath.Dir(path))
		return true, nil
	case errors.Is(err, fs.ErrExist):
		return false, nil // outro processo chegou primeiro
	default:
		// Filesystem sem suporte a hard link: recai para o O_EXCL direto, que
		// continua sendo a garantia de exclusão mútua exigida aqui.
		return createExclusiveDirect(path, data, perm)
	}
}

// createExclusiveDirect é o plano B de CreateExclusive.
func createExclusiveDirect(path string, data []byte, perm os.FileMode) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := writeSyncClose(f, data, perm); err != nil {
		return false, err
	}
	syncDir(filepath.Dir(path))
	return true, nil
}

// ReplaceFile substitui o conteúdo de path de forma atômica.
func (osSystem) ReplaceFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := writeTemp(path, data, perm)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// writeTemp grava data num arquivo temporário no MESMO diretório de path
// (requisito para que link/rename sejam atômicos) e devolve o caminho dele.
func writeTemp(path string, data []byte, perm os.FileMode) (string, error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err := writeSyncClose(f, data, perm); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// writeSyncClose grava, ajusta a permissão, descarrega no disco e fecha.
//
// O Chmod é explícito porque a permissão passada ao open(2) ainda é mascarada
// pelo umask do processo: 0644 seria apenas um teto, e num container com umask
// restritivo o arquivo de identidade sairia 0600, ilegível para um sidecar com
// outro UID. O Sync garante a durabilidade que um arquivo de identidade exige:
// sem ele, um kernel panic logo após o boot deixaria o arquivo com zero bytes
// e o próximo boot trocaria a identidade do nó.
func writeSyncClose(f *os.File, data []byte, perm os.FileMode) error {
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// syncDir descarrega a entrada de diretório, para que a criação do arquivo
// sobreviva a uma queda de energia. É best-effort: em alguns sistemas (Windows,
// entre outros) abrir um diretório para Sync não é suportado, e falhar aqui
// seria pior do que seguir com o arquivo já gravado e sincronizado.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

func (osSystem) Remove(path string) error { return os.Remove(path) }

func (osSystem) Hostname() (string, error) { return os.Hostname() }

func (osSystem) Args() []string { return os.Args }

// GenerateUUIDv7 gera um UUIDv7 no nível 1 (precisão de milissegundos),
// o formato padrão e 100% compatível com a RFC 9562.
func (osSystem) GenerateUUIDv7() (string, error) {
	return loghubuuid.GenerateString(loghubuuid.Level1), nil
}
