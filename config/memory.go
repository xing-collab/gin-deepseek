package config

const maxHistoryMessages = 20

// AddHistory appends one Chat Completions message and trims old turns.
func (llm *LLM) AddHistory(message map[string]any) []map[string]any {
	llm.history = append(llm.history, message)
	if len(llm.history) > maxHistoryMessages {
		llm.history = append([]map[string]any(nil), llm.history[2:]...)
	}
	return llm.history
}

func (llm *LLM) SetSystemPrompt(prompt string) {
	llm.systemPrompt = prompt
}

func (llm *LLM) snapshot(prompt, content string) []map[string]any {
	if prompt != "" {
		llm.systemPrompt = prompt
	}
	llm.AddHistory(map[string]any{"role": "user", "content": content})
	messages := make([]map[string]any, 0, len(llm.history)+1)
	messages = append(messages, map[string]any{
		"role":    "system",
		"content": llm.systemPrompt,
	})
	messages = append(messages, llm.history...)
	return messages
}

// History returns a deep copy of the Responses API conversation history.
func (c *OpenAPIClient) History() []map[string]string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return copyHistory(c.history)
}

func (c *OpenAPIClient) AddHistory(message map[string]string) []map[string]string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addHistoryLocked(message)
	return copyHistory(c.history)
}

func (c *OpenAPIClient) ClearHistory() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.history = nil
}

func (c *OpenAPIClient) addHistoryLocked(message map[string]string) {
	c.history = append(c.history, message)
	if len(c.history) > maxHistoryMessages {
		c.history = append(
			[]map[string]string(nil),
			c.history[len(c.history)-maxHistoryMessages:]...,
		)
	}
}

func copyHistory(history []map[string]string) []map[string]string {
	out := make([]map[string]string, 0, len(history))
	for _, message := range history {
		copyMessage := make(map[string]string, len(message))
		for key, value := range message {
			copyMessage[key] = value
		}
		out = append(out, copyMessage)
	}
	return out
}

func (c *OpenAPIClient) appendUser(content string) []map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addHistoryLocked(map[string]string{"role": "user", "content": content})
	return copyHistory(c.history)
}

func (c *OpenAPIClient) appendAssistant(content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addHistoryLocked(map[string]string{"role": "assistant", "content": content})
}
