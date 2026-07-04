package agent

type openCodeAdapter struct{}

func (openCodeAdapter) Invocation(baseArgs []string, prompt, sessionID string) Invocation {
	args := append([]string{}, baseArgs...)
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}
	args = ensureFlagValue(args, "--format", "json")
	args = append(args, prompt)
	return Invocation{Args: args, SessionID: sessionID}
}

func (openCodeAdapter) ExtractSessionID(output string) string {
	return extractSessionID(output)
}
