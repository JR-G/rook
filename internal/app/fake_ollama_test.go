package app

import (
	"context"

	"github.com/JR-G/rook/internal/ollama"
)

func (f fakeOllama) ChatStructured(
	context.Context,
	string,
	[]ollama.Message,
	float64,
	any,
) (ollama.ChatResult, error) {
	return ollama.ChatResult{
		Model:   "phi4-mini",
		Content: `{"answer":"ok"}`,
	}, nil
}
