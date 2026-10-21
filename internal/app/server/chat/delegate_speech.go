package chat

// InjectDelegateSpeech speaks a background task result without the LLM.
// onSpoken is called once, from the LLM queue goroutine, after the turn ends:
// played is false when the user interrupted or the session closed. It is not
// called when the text could not be queued; the returned error says so.
func (c *ChatManager) InjectDelegateSpeech(text string, onSpoken func(played bool)) error {
	c.cancelRetainedSessionCleanup("delegate_result")
	if err := c.prepareSpeakPathForInjectedSpeech(text, false); err != nil {
		return err
	}
	session, err := c.ensureSession()
	if err != nil {
		return err
	}
	return session.AddTextToTTSQueueWithOptions(text, llmResponseChannelOptions{
		onTTSPlaybackStart: c.newInjectedSpeechStartHook(),
		ttsTurnEndPolicy:   injectedSpeechTTSTurnEndPolicy(false),
		onSpoken:           onSpoken,
	})
}

// IsConversationActive reports whether the device is listening, thinking or
// speaking, so background announcements can wait for the turn to end.
func (c *ChatManager) IsConversationActive() bool {
	return c.isConversationActive()
}
