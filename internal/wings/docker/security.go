package docker

// securityOpts are the security options of every Raptor container:
// no-new-privileges and Wings' seccomp profile (seccomp_linux.go).
func securityOpts() []string {
	opts := []string{"no-new-privileges"}
	if p := SeccompProfile(); p != "" {
		opts = append(opts, "seccomp="+p)
	}
	return opts
}
