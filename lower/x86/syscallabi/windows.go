package syscallabi

// Windows has no stable, documented int 0x80/sysenter convention for
// user-mode syscalls: the numbers and calling sequence change per build and
// are normally reached only through ntdll, never a bare trap. Deliberately
// left unregistered — syscallabi.Lookup("windows") reports ok == false, and
// x86.selSyscall surfaces that as an explicit lowering error rather than
// guessing at a convention. TODO(§4): revisit if a syscall.<type> use case
// on Windows ever needs first-class support.