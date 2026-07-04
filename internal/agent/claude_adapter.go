package agent

import "github.com/google/uuid"

type claudeAdapter struct{}

func (claudeAdapter) Invocation(baseArgs []string, prompt, sessionID string) Invocation {
	args := append([]string{}, baseArgs...)
	if sessionID != "" {
		args = append(args, "--resume", sessionID, prompt)
		return Invocation{Args: args, SessionID: sessionID}
	}
	generated := uuid.NewString()
	if generated != "" && !hasFlag(args, "--session-id") {
		args = append(args, "--session-id", generated)
	}
	args = append(args, prompt)
	return Invocation{Args: args, SessionID: generated}
}

func (claudeAdapter) ExtractSessionID(output string) string {
	return extractSessionID(output)
}
