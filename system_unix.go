//go:build unix

package loghubident

import (
	"os"
	"syscall"
)

// openRegular abre um arquivo sem bloquear em FIFOs sem escritor.
// No Unix, O_NONBLOCK garante que a chamada retorne de imediato caso a entrada
// tenha sido trocada por um pipe na janela entre stat e open (TOCTOU).
//
// Com follow=false o kernel também recusa um link simbólico no último
// componente (O_NOFOLLOW, onde o syscall do Go expõe a flag — ver
// openNoFollow). É a primeira barreira contra a troca da entrada por um link
// entre o Lstat e o Open; a segunda é o os.SameFile de readRegular.
func openRegular(path string, follow bool) (*os.File, error) {
	flags := syscall.O_RDONLY | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	if !follow {
		flags |= openNoFollow
	}
	fd, err := syscall.Open(path, flags, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	// Limpa O_NONBLOCK para leitura síncrona comum de arquivo regular
	_ = syscall.SetNonblock(fd, false)
	return f, nil
}
