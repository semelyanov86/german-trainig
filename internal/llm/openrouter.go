package llm

import "log"

const openRouterChatEndpoint = "https://openrouter.ai/api/v1/chat/completions"

// newOpenRouter builds the openrouter.ai backend. Unlike polza it reports cost
// only when the request opts in, hence includeUsage.
func newOpenRouter(s Spec, logger *log.Logger) *openAIChatProvider {
	return newOpenAIChat(chatBackend{
		name:         "OpenRouter",
		endpoint:     openRouterChatEndpoint,
		apiKey:       s.OpenRouterAPIKey,
		includeUsage: true,
	}, s, logger)
}
