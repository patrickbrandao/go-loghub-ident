//go:build !unix

package loghubident

import "os"

// openRegular abre um arquivo de forma padrão em plataformas não-Unix (ex.: Windows),
// onde pipes nomeados POSIX e a semântica de bloqueio de open() em FIFOs não se aplicam.
func openRegular(path string) (*os.File, error) {
	return os.Open(path)
}
