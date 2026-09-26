package configfile

import "strings"

// editText implements the "file" parser: every line starting with a rule's
// key is replaced by the rule's value (which normally repeats the key, e.g.
// "server.port 28015"). Conditions don't apply, as in Pterodactyl.
func editText(data []byte, rules []Rule) []byte {
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		cr := strings.HasSuffix(line, "\r")
		for _, r := range rules {
			if strings.HasPrefix(line, r.Key) {
				line = r.String()
			}
		}
		if cr && !strings.HasSuffix(line, "\r") {
			line += "\r"
		}
		lines[i] = line
	}
	return []byte(strings.Join(lines, "\n"))
}
