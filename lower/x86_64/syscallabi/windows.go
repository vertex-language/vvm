package syscallabi

// Windows is intentionally left unregistered: there's no stable,
// documented user-mode syscall convention on x86_64 Windows, so
// Lookup("windows") reports ok == false and the caller surfaces that as an
// explicit lowering error.