//go:build unix && !linux && !darwin && !freebsd && !netbsd && !openbsd

package loghubident

// openNoFollow é zero onde o pacote syscall não expõe O_NOFOLLOW (Solaris,
// illumos, AIX, DragonFly). A recusa de links simbólicos continua garantida
// pelo Lstat prévio e pelo os.SameFile posterior de readRegular.
const openNoFollow = 0

// isSymlinkRefusal nunca reconhece nada aqui: sem O_NOFOLLOW o open não
// devolve um erro específico de link simbólico.
func isSymlinkRefusal(error) bool { return false }
