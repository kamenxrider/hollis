package cli

import (
	"context"
	"errors"
	"io"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

type generationFlags struct {
	temperature float64
	topP        float64
	maxTokens   int
	seed        uint64
}

func (f *generationFlags) addTo(cmd *cobra.Command) {
	cmd.Flags().Float64Var(&f.temperature, "temperature", 0, "Local temperature in 0..2; zero selects greedy sampling")
	cmd.Flags().Float64Var(&f.topP, "top-p", 1, "Local sampling probability threshold in (0,1]")
	cmd.Flags().IntVar(&f.maxTokens, "max-tokens", 0, "Local maximum response tokens per model decision")
	cmd.Flags().Uint64Var(&f.seed, "seed", 0, "Local UInt64 random sampling seed")
}

func (f *generationFlags) options(cmd *cobra.Command, model runner.Model) (*runner.GenerationOptions, error) {
	options := &runner.GenerationOptions{}
	present := false
	for _, field := range []struct {
		name string
		set  func()
	}{
		{"temperature", func() { options.Temperature = &f.temperature }},
		{"top-p", func() { options.TopP = &f.topP }},
		{"max-tokens", func() { options.MaxTokens = &f.maxTokens }},
		{"seed", func() { options.Seed = &f.seed }},
	} {
		if cmd.Flags().Changed(field.name) {
			present = true
			field.set()
		}
	}
	if !present {
		return nil, nil
	}
	if model != runner.ModelLocal {
		return nil, usageErr(errors.New("sampling flags require --model local"))
	}
	if err := options.Validate(); err != nil {
		return nil, usageErr(err)
	}
	return options, nil
}

func runLocalGeneration(ctx context.Context, r runner.Runner, request runner.GenerationRequest, streaming bool, out io.Writer) (runner.Completion, error) {
	generation, ok := r.(runner.GenerationRunner)
	if !ok {
		return runner.Completion{}, errors.New("configured runner does not support native-local sampling")
	}
	if !streaming {
		return generation.RunGeneration(ctx, runner.ModelLocal, request, nil)
	}
	safe := &terminalStreamWriter{out: out}
	completion, err := generation.RunGeneration(ctx, runner.ModelLocal, request, func(delta string) error {
		_, err := safe.Write([]byte(delta))
		return err
	})
	flushErr := safe.Flush()
	if err != nil {
		return completion, err
	}
	return completion, flushErr
}
