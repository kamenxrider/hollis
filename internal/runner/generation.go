package runner

import "context"

// GenerationRequest carries optional Local sampling without changing other routes.
type GenerationRequest struct {
	Prompt  string
	Options *GenerationOptions
	Schema  *Schema
	Images  []string
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
		return r.invokeImages(ctx, model, request.Prompt, request.Images, emit, request.Options)
	}
	operation := "complete"
	if request.Schema != nil {
		operation = "schema"
	} else if emit != nil {
		operation = "stream"
	}
	result, _, err := r.invoke(ctx, model, operation, request.Prompt, emit, request.Schema, nativeOptions{GenerationOptions: request.Options})
	return result, err
}

func (r *RoutedRunner) RunGeneration(ctx context.Context, model Model, request GenerationRequest, emit func(string) error) (Completion, error) {
	if model != ModelLocal {
		return Completion{}, nativeError(KindUsage, "sampling is supported only for model local")
	}
	return r.Native.RunGeneration(ctx, model, request, emit)
}
