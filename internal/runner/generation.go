package runner

import (
	"context"
	"github.com/kamenxrider/hollis/internal/store"
)

// GenerationRequest carries optional Local sampling without changing other routes.
type GenerationRequest struct {
	Instructions string
	History      []store.Message
	Permissive   bool
	Prompt       string
	Options      *GenerationOptions
	Schema       *Schema
	Images       []string
}

type GenerationRunner interface {
	RunGeneration(context.Context, Model, GenerationRequest, func(string) error) (Completion, error)
}

func (r *NativeRunner) RunGeneration(ctx context.Context, model Model, request GenerationRequest, emit func(string) error) (Completion, error) {
	if err := request.Options.Validate(); err != nil {
		return Completion{}, err
	}
	if request.Schema != nil && (emit != nil || len(request.Images) > 0) {
		return Completion{}, nativeError(KindUsage, "guided output is complete-only and text-only")
	}
	if len(request.Images) > 0 {
		staged, cleanup, err := stageImageInputs(request.Images)
		if err != nil {
			return Completion{}, nativeImageStagingError(err)
		}
		defer cleanup()
		if len(staged) != NativeMaxImages {
			return Completion{}, nativeError(KindUsage, "local accepts exactly 1 image; the maximum is 1")
		}
		operation := "complete"
		if emit != nil {
			operation = "stream"
		}
		result, _, err := r.invoke(ctx, model, operation, request.Prompt, emit, nil, nativeOptions{
			GenerationOptions: request.Options, Instructions: request.Instructions, History: request.History,
			Permissive: request.Permissive, Images: []nativeSource{{Path: staged[0]}},
		})
		return result, err
	}
	operation := "complete"
	if request.Schema != nil {
		operation = "schema"
	} else if emit != nil {
		operation = "stream"
	}
	result, _, err := r.invoke(ctx, model, operation, request.Prompt, emit, request.Schema, nativeOptions{
		GenerationOptions: request.Options, Instructions: request.Instructions, History: request.History, Permissive: request.Permissive,
	})
	return result, err
}

type TokenCount struct {
	Tokens        int  `json:"tokens"`
	ContextSize   int  `json:"context_size"`
	AnswerReserve int  `json:"answer_reserve"`
	Fits          bool `json:"fits"`
}

type TokenCountRunner interface {
	CountTokens(context.Context, GenerationRequest, int) (TokenCount, error)
}

func (r *NativeRunner) CountTokens(ctx context.Context, request GenerationRequest, reserve int) (TokenCount, error) {
	result, status, err := r.invoke(ctx, ModelLocal, "count_tokens", request.Prompt, nil, nil, nativeOptions{Instructions: request.Instructions, History: request.History})
	if err != nil {
		return TokenCount{}, err
	}
	if result.countedTokens == nil || status.ContextSize <= 0 {
		return TokenCount{}, nativeError(KindNativeProtocol, "native token count lacks measured counts")
	}
	return TokenCount{Tokens: *result.countedTokens, ContextSize: status.ContextSize, AnswerReserve: reserve, Fits: *result.countedTokens+reserve <= status.ContextSize}, nil
}

func (r *RoutedRunner) CountTokens(ctx context.Context, request GenerationRequest, reserve int) (TokenCount, error) {
	return r.Native.CountTokens(ctx, request, reserve)
}

func (r *RoutedRunner) RunGeneration(ctx context.Context, model Model, request GenerationRequest, emit func(string) error) (Completion, error) {
	if model != ModelLocal {
		return Completion{}, nativeError(KindUsage, "sampling is supported only for model local")
	}
	return r.Native.RunGeneration(ctx, model, request, emit)
}
