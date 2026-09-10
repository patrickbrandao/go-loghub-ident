//go:build unix

package loghubident

import (
	"os"
	"syscall"
)

// openRegular abre um arquivo sem bloquear em FIFOs sem escritor.
// No Unix, O_NONBLOCK garante que a chamada retorne de imediato caso a entrada
// tenha sido trocada por um pipe na janela entre stat e open (TOCTOU).
func openRegular(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	// Limpa O_NONBLOCK para leitura síncrona comum de arquivo regular
	_ = syscall.SetNonblock(fd, false)
	return f, nil
}
