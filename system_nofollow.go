//go:build linux || darwin || freebsd || netbsd || openbsd

package loghubident

import (
	"errors"
	"syscall"
)

// openNoFollow é a flag que faz open(2) recusar um link simbólico no último
// componente do caminho. Nestes sistemas o pacote syscall a expõe.
const openNoFollow = syscall.O_NOFOLLOW

// isSymlinkRefusal reconhece o erro com que open(2) recusa um link sob
// O_NOFOLLOW: ELOOP em Linux, macOS, NetBSD e OpenBSD; EMLINK em FreeBSD.
func isSymlinkRefusal(err error) bool {
	return errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK)
}
