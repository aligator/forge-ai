package agent

type codexAdapter struct{}

func (codexAdapter) Invocation(baseArgs []string, prompt, sessionID string) Invocation {
	args := append([]string{}, baseArgs...)
	args = ensureFlag(args, "--json")
	if sessionID != "" {
		args = appendExecSubcommand(args, "resume", sessionID)
	}
	args = append(args, prompt)
	return Invocation{Args: args, SessionID: sessionID}
}

func (codexAdapter) ExtractSessionID(output string) string {
	return extractSessionID(output)
}
