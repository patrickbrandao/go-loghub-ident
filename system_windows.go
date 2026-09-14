//go:build !unix

package loghubident

import "os"

// openRegular abre um arquivo de forma padrão em plataformas não-Unix (ex.: Windows),
// onde pipes nomeados POSIX e a semântica de bloqueio de open() em FIFOs não se aplicam.
// A recusa de links simbólicos (follow=false) fica a cargo do Lstat prévio e do
// os.SameFile posterior de readRegular.
func openRegular(path string, _ bool) (*os.File, error) {
	return os.Open(path)
}

// isSymlinkRefusal nunca reconhece nada aqui: não há O_NOFOLLOW.
func isSymlinkRefusal(error) bool { return false }
