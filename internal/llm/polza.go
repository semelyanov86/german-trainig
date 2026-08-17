package llm

import "log"

const polzaChatEndpoint = "https://polza.ai/api/v1/chat/completions"

// newPolza builds the polza.ai backend. Cost comes back in the usage object
// unasked, as cost_rub.
func newPolza(s Spec, logger *log.Logger) *openAIChatProvider {
	return newOpenAIChat(chatBackend{
		name:     "Polza",
		endpoint: polzaChatEndpoint,
		apiKey:   s.PolzaAPIKey,
	}, s, logger)
}
