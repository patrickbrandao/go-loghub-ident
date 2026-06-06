package loghubident

import (
	"os"

	loghubuuid "github.com/patrickbrandao/go-loghub-uuid"
)

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

	// ReadFile lê o conteúdo completo de um arquivo.
	ReadFile(path string) ([]byte, error)

	// WriteFile grava data no arquivo com a permissão informada.
	WriteFile(path string, data []byte, perm os.FileMode) error

	// Hostname retorna o nome de host do sistema.
	Hostname() (string, error)

	// Args retorna os argumentos do processo (equivalente a os.Args).
	Args() []string

	// GenerateUUIDv7 gera um UUIDv7 canônico (com hífens).
	//
	// A assinatura inclui error para permitir que os testes simulem uma
	// falha de geração (código de saída 105). A implementação real nunca
	// retorna erro, pois loghubuuid.GenerateString não falha.
	GenerateUUIDv7() (string, error)
}

// osSystem é a implementação de system baseada no sistema operacional real.
// É um struct vazio e sem estado, seguro para uso concorrente.
type osSystem struct{}

func (osSystem) Getenv(key string) string { return os.Getenv(key) }

func (osSystem) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }

func (osSystem) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (osSystem) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func (osSystem) Hostname() (string, error) { return os.Hostname() }

func (osSystem) Args() []string { return os.Args }

// GenerateUUIDv7 gera um UUIDv7 no nível 1 (precisão de milissegundos),
// o formato padrão e 100% compatível com a RFC 9562.
func (osSystem) GenerateUUIDv7() (string, error) {
	return loghubuuid.GenerateString(loghubuuid.Level1), nil
}
